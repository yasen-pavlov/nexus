// Command rag-eval is the offline RAG quality harness (`make rag-eval`).
//
// It wires the live RAG stack (DB + OpenSearch + LLM providers, exactly as
// the server does), runs each golden case under internal/rag/testdata/golden
// through the orchestrator, scores it with internal/rag/eval (citation check
// + LLM-as-judge), and writes a markdown report diffed against the previous
// baseline. It needs the same env as the server (NEXUS_DATABASE_URL,
// NEXUS_OPENSEARCH_URL, the LLM provider keys, …).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/muty/nexus/internal/api"
	"github.com/muty/nexus/internal/config"
	"github.com/muty/nexus/internal/crypto"
	"github.com/muty/nexus/internal/lang"
	"github.com/muty/nexus/internal/llm"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/rag"
	"github.com/muty/nexus/internal/rag/eval"
	"github.com/muty/nexus/internal/search"
	"github.com/muty/nexus/internal/storage"
	"github.com/muty/nexus/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rag-eval:", err)
		os.Exit(1)
	}
}

func run() error {
	goldenDir := flag.String("golden", "internal/rag/testdata/golden", "directory of golden YAML cases")
	userName := flag.String("user", "muty", "username whose indexed corpus to query")
	runModel := flag.String("model", "", "model id for answers (empty = configured LLM default)")
	judgeModel := flag.String("judge-model", "", "model id for the judge (empty = configured LLM default)")
	reportOut := flag.String("out", "rag-eval-report.md", "markdown report output path")
	flag.Parse()

	ctx := context.Background()
	log := zap.NewNop() // the report is the output; keep the orchestrator quiet

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	st, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.Close()

	stack, err := buildStack(ctx, cfg, st, log)
	if err != nil {
		return err
	}
	lm := stack.lm

	user, _, err := st.GetUserByUsername(ctx, *userName)
	if err != nil {
		return fmt.Errorf("look up user %q: %w", *userName, err)
	}

	runLabel := *runModel
	if runLabel == "" {
		runLabel = lm.DefaultModel()
	}
	judge := *judgeModel
	if judge == "" {
		judge = lm.DefaultModel()
	}
	judgeGen, err := makeJudgeGen(lm.Get(), judge)
	if err != nil {
		return err
	}

	runner := makeRunner(stack.orch, st, stack.searchClient, user.ID, *runModel)

	cases, err := eval.LoadGolden(*goldenDir)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return fmt.Errorf("no golden cases found in %s", *goldenDir)
	}
	fmt.Fprintf(os.Stderr, "running %d golden cases as %q (model=%s, judge=%s)…\n",
		len(cases), *userName, runLabel, judge)

	rep := eval.RunSuite(ctx, cases, runner, judgeGen, runLabel, judge)

	baselinePath := filepath.Join(*goldenDir, ".baseline.json")
	prev, err := eval.LoadBaseline(baselinePath)
	if err != nil {
		return fmt.Errorf("load baseline: %w", err)
	}
	md := eval.RenderMarkdown(rep, prev)
	if err := os.WriteFile(*reportOut, []byte(md), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := eval.SaveBaseline(baselinePath, rep); err != nil {
		return fmt.Errorf("save baseline: %w", err)
	}
	fmt.Print(md)
	fmt.Fprintf(os.Stderr, "\nreport written to %s — passed %d/%d\n", *reportOut, rep.PassCount(), len(rep.Results))
	return nil
}

// openStore connects to Postgres and installs the AES encryption key when
// one is configured. Settings (provider API keys) are stored encrypted, so
// without the key GetSettings hands the ciphertext to the provider and every
// call 401s.
func openStore(ctx context.Context, cfg *config.Config, log *zap.Logger) (*store.Store, error) {
	st, err := store.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}
	if cfg.EncryptionKey != "" {
		key, err := crypto.NewKey(cfg.EncryptionKey)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("encryption key: %w", err)
		}
		st.SetEncryptionKey(key)
	}
	return st, nil
}

// evalStack groups the live RAG dependencies the harness wires up so run
// can hand them to the runner + judge.
type evalStack struct {
	lm           *api.LLMManager
	searchClient *search.Client
	orch         *rag.Orchestrator
}

// buildStack loads every settings manager from the DB and wires the live
// search service + orchestrator exactly as the server does.
func buildStack(ctx context.Context, cfg *config.Config, st *store.Store, log *zap.Logger) (evalStack, error) {
	em := api.NewEmbeddingManager(st, log)
	if err := em.LoadFromDB(ctx, cfg); err != nil {
		return evalStack{}, fmt.Errorf("load embedding settings: %w", err)
	}
	rm := api.NewRerankManager(st, log)
	if err := rm.LoadFromDB(ctx, cfg); err != nil {
		return evalStack{}, fmt.Errorf("load rerank settings: %w", err)
	}
	rankingMgr := api.NewRankingManager(st, log)
	if err := rankingMgr.LoadFromDB(ctx); err != nil {
		return evalStack{}, fmt.Errorf("load ranking settings: %w", err)
	}
	lm := api.NewLLMManager(st, log)
	if err := lm.LoadFromDB(ctx, cfg); err != nil {
		return evalStack{}, fmt.Errorf("load llm settings: %w", err)
	}
	ragMgr := api.NewRAGManager(st, log)
	if err := ragMgr.LoadFromDB(ctx, cfg); err != nil {
		return evalStack{}, fmt.Errorf("load rag settings: %w", err)
	}

	searchClient, err := search.New(ctx, cfg.OpenSearchURL, log, lang.Default(), search.WithAuth(search.AuthConfig{
		Username:   cfg.OpenSearchUsername,
		Password:   cfg.OpenSearchPassword,
		CAFile:     cfg.OpenSearchCAFile,
		SkipVerify: cfg.OpenSearchInsecureSkipVerify,
	}))
	if err != nil {
		return evalStack{}, fmt.Errorf("connect opensearch: %w", err)
	}
	binaryStore, err := storage.New(cfg.BinaryStorePath, st, log)
	if err != nil {
		return evalStack{}, fmt.Errorf("init binary store: %w", err)
	}

	searchService := api.NewSearchService(searchClient, em, rm, rankingMgr, log)
	orch := rag.NewOrchestrator(rag.Deps{
		Registry: lm.Get,
		Settings: func() rag.Settings {
			return rag.Settings{
				RewriterModel:        lm.RewriterModel(),
				MaxToolRounds:        ragMgr.MaxToolRounds(),
				MaxImagesPerTurn:     ragMgr.MaxImagesPerTurn(),
				EnableMultimodal:     ragMgr.EnableMultimodal(),
				EnableOpenAttachment: ragMgr.EnableOpenAttachment(),
			}
		},
		Search:      api.NewRAGSearchProvider(searchService),
		Chats:       st,
		Cfg:         rag.DefaultConfig(),
		Log:         log,
		Binaries:    binaryStore,
		Attachments: searchClient,
	})

	return evalStack{lm: lm, searchClient: searchClient, orch: orch}, nil
}

// makeRunner returns a TurnRunner that creates an ephemeral chat, runs one
// turn through the orchestrator, drains the event stream into a TurnOutput,
// and deletes the chat afterwards.
func makeRunner(orch *rag.Orchestrator, st *store.Store, sc *search.Client, userID uuid.UUID, modelID string) eval.TurnRunner {
	return func(ctx context.Context, query string) (eval.TurnOutput, error) {
		chat := &model.Chat{UserID: userID}
		if err := st.CreateChat(ctx, chat); err != nil {
			return eval.TurnOutput{}, fmt.Errorf("create chat: %w", err)
		}
		defer func() { _ = st.DeleteChat(context.Background(), chat.ID) }()

		events, err := orch.Run(ctx, rag.RunInput{
			ChatID: chat.ID, UserID: userID, Content: query, Model: modelID,
		})
		if err != nil {
			return eval.TurnOutput{}, err
		}
		out, evidenceIDs, runErr := drainTurnEvents(events)
		if runErr != "" {
			return out, fmt.Errorf("orchestrator: %s", runErr)
		}
		// Fetch the FULL text of the retrieved chunks for the faithfulness
		// judge — the streamed previews are highlighted snippets, too thin
		// to verify specific figures against.
		out.Evidence = fetchEvidenceText(ctx, sc, evidenceIDs)
		return out, nil
	}
}

// drainTurnEvents consumes the orchestrator's event stream into a
// TurnOutput, collecting the answer text, deduped cited doc ids, and the
// deduped evidence ids (in first-seen order) for later full-text fetch. The
// returned string is the orchestrator error message, if any.
func drainTurnEvents(events <-chan rag.Event) (eval.TurnOutput, []string, string) {
	c := turnCollector{cited: map[string]struct{}{}, evidence: map[string]struct{}{}}
	for ev := range events {
		switch ev.Kind {
		case rag.EvText:
			c.out.Answer += ev.TextDelta
		case rag.EvCitation:
			if ev.Citation != nil {
				c.noteCitation(ev.Citation.DocID)
			}
		case rag.EvEvidence:
			for _, ch := range ev.Evidence {
				c.noteEvidence(ch.DocID)
			}
		case rag.EvToolResult:
			for _, ch := range ev.ToolChunks {
				c.noteEvidence(ch.DocID)
			}
		case rag.EvError:
			c.runErr = ev.Err
		}
	}
	return c.out, c.evidenceIDs, c.runErr
}

// turnCollector accumulates the answer text, deduped cited doc ids, and
// deduped evidence ids (in first-seen order) while draining an orchestrator
// event stream.
type turnCollector struct {
	out         eval.TurnOutput
	runErr      string
	cited       map[string]struct{}
	evidence    map[string]struct{}
	evidenceIDs []string
}

// noteCitation records a cited doc id once, preserving first-seen order.
func (c *turnCollector) noteCitation(docID string) {
	if _, dup := c.cited[docID]; dup {
		return
	}
	c.cited[docID] = struct{}{}
	c.out.CitedDocIDs = append(c.out.CitedDocIDs, docID)
}

// noteEvidence records an evidence doc id once, preserving first-seen order.
func (c *turnCollector) noteEvidence(docID string) {
	if docID == "" {
		return
	}
	if _, dup := c.evidence[docID]; dup {
		return
	}
	c.evidence[docID] = struct{}{}
	c.evidenceIDs = append(c.evidenceIDs, docID)
}

// fetchEvidenceText resolves chunk ids to their full content (truncated)
// for the judge. Best-effort: a chunk that no longer resolves is skipped.
func fetchEvidenceText(ctx context.Context, sc *search.Client, docIDs []string) []string {
	const maxPerChunk = 1500
	var out []string
	for _, id := range docIDs {
		chunk, err := sc.GetChunkByDocID(ctx, id)
		if err != nil || chunk == nil {
			continue
		}
		body := chunk.FullContent
		if body == "" {
			body = chunk.Content
		}
		if len(body) > maxPerChunk {
			body = body[:maxPerChunk]
		}
		title := chunk.Title
		if title == "" {
			title = id
		}
		out = append(out, title+":\n"+body)
	}
	return out
}

// makeJudgeGen wraps a model's streaming generator as a one-shot
// system+user → text call for the LLM-as-judge.
func makeJudgeGen(reg llm.Registry, modelID string) (eval.GenerateFunc, error) {
	gen, info, err := reg.Get(modelID)
	if err != nil {
		return nil, fmt.Errorf("resolve judge model %q: %w", modelID, err)
	}
	bare := info.BareID
	if bare == "" {
		bare = modelID
	}
	return func(ctx context.Context, system, user string) (string, error) {
		events, err := gen.Generate(ctx, llm.GenerateRequest{
			Model:     bare,
			System:    system,
			Messages:  []llm.Message{{Role: llm.RoleUser, Content: user}},
			MaxTokens: 256,
		})
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for e := range events {
			switch e.Kind {
			case llm.EventText:
				sb.WriteString(e.TextDelta)
			case llm.EventError:
				if e.Err != nil {
					return "", e.Err
				}
			}
		}
		return sb.String(), nil
	}, nil
}
