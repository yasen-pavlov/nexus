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
	st, err := store.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer st.Close()

	// Settings (provider API keys) are stored AES-encrypted; install the
	// key so GetSettings decrypts them on read — without this the manager
	// hands the ciphertext to the provider and every call 401s.
	if cfg.EncryptionKey != "" {
		key, err := crypto.NewKey(cfg.EncryptionKey)
		if err != nil {
			return fmt.Errorf("encryption key: %w", err)
		}
		st.SetEncryptionKey(key)
	}

	em := api.NewEmbeddingManager(st, log)
	if err := em.LoadFromDB(ctx, cfg); err != nil {
		return fmt.Errorf("load embedding settings: %w", err)
	}
	rm := api.NewRerankManager(st, log)
	if err := rm.LoadFromDB(ctx, cfg); err != nil {
		return fmt.Errorf("load rerank settings: %w", err)
	}
	rankingMgr := api.NewRankingManager(st, log)
	if err := rankingMgr.LoadFromDB(ctx); err != nil {
		return fmt.Errorf("load ranking settings: %w", err)
	}
	lm := api.NewLLMManager(st, log)
	if err := lm.LoadFromDB(ctx, cfg); err != nil {
		return fmt.Errorf("load llm settings: %w", err)
	}
	ragMgr := api.NewRAGManager(st, log)
	if err := ragMgr.LoadFromDB(ctx, cfg); err != nil {
		return fmt.Errorf("load rag settings: %w", err)
	}

	searchClient, err := search.New(ctx, cfg.OpenSearchURL, log, lang.Default())
	if err != nil {
		return fmt.Errorf("connect opensearch: %w", err)
	}
	binaryStore, err := storage.New(cfg.BinaryStorePath, st, log)
	if err != nil {
		return fmt.Errorf("init binary store: %w", err)
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

	runner := makeRunner(orch, st, searchClient, user.ID, *runModel)

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
		var out eval.TurnOutput
		var runErr string
		citedSeen := map[string]struct{}{}
		evidenceSeen := map[string]struct{}{}
		var evidenceIDs []string
		note := func(docID string) {
			if docID == "" {
				return
			}
			if _, dup := evidenceSeen[docID]; !dup {
				evidenceSeen[docID] = struct{}{}
				evidenceIDs = append(evidenceIDs, docID)
			}
		}
		for ev := range events {
			switch ev.Kind {
			case rag.EvText:
				out.Answer += ev.TextDelta
			case rag.EvCitation:
				if ev.Citation != nil {
					if _, dup := citedSeen[ev.Citation.DocID]; !dup {
						citedSeen[ev.Citation.DocID] = struct{}{}
						out.CitedDocIDs = append(out.CitedDocIDs, ev.Citation.DocID)
					}
				}
			case rag.EvEvidence:
				for _, c := range ev.Evidence {
					note(c.DocID)
				}
			case rag.EvToolResult:
				for _, c := range ev.ToolChunks {
					note(c.DocID)
				}
			case rag.EvError:
				runErr = ev.Err
			}
		}
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
