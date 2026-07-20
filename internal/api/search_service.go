package api

import (
	"context"

	"github.com/muty/nexus/internal/embedding"
	"github.com/muty/nexus/internal/model"
	"github.com/muty/nexus/internal/search"
	"go.uber.org/zap"
)

// SearchService runs the post-retrieval ranking pipeline shared by the
// HTTP search handler and the RAG orchestrator. It owns retrieve →
// rerank → trust → recency-decay → metadata-bonus → window-matches.
//
// Pagination + JSON serialisation stay at the caller (the HTTP handler
// paginates; the RAG orchestrator asks for top-N upfront via req.Limit).
type SearchService struct {
	search  *search.Client
	em      *EmbeddingManager
	rm      *RerankManager
	ranking *RankingManager
	log     *zap.Logger
}

// SearchOptions toggles per-request features. IncludeScoreDetails seeds
// each hit's ScoreDetails with retrieval+reranker scores so the UI can
// render the explain panel.
type SearchOptions struct {
	IncludeScoreDetails bool
}

// NewSearchService wires the pipeline against the same managers the
// HTTP handler uses. All inputs may be the live managers; tests can
// pass nil-safe stand-ins (rankingConfig falls back to defaults).
func NewSearchService(s *search.Client, em *EmbeddingManager, rm *RerankManager, ranking *RankingManager, log *zap.Logger) *SearchService {
	return &SearchService{
		search:  s,
		em:      em,
		rm:      rm,
		ranking: ranking,
		log:     log,
	}
}

// RAGSearchProvider adapts *SearchService to the simpler signature
// the RAG orchestrator depends on. The orchestrator never asks for
// score details, so this thin wrapper drops the option.
type RAGSearchProvider struct {
	svc *SearchService
}

// NewRAGSearchProvider returns an adapter usable as rag.SearchProvider.
func NewRAGSearchProvider(svc *SearchService) *RAGSearchProvider {
	return &RAGSearchProvider{svc: svc}
}

// Run dispatches to the underlying SearchService with score-detail
// reporting disabled.
func (p *RAGSearchProvider) Run(ctx context.Context, req model.SearchRequest) (*model.SearchResult, error) {
	return p.svc.Run(ctx, req, SearchOptions{IncludeScoreDetails: false})
}

// Run executes retrieve → rerank → trust weights → recency decay →
// metadata bonus → window matches. Pagination is the caller's job.
func (s *SearchService) Run(ctx context.Context, req model.SearchRequest, opts SearchOptions) (*model.SearchResult, error) {
	// Stage 1: retrieve candidates. Hybrid (BM25 + kNN) when an embedder
	// is wired; BM25-only otherwise. Returns the FULL deduped pool — the
	// caller paginates after the pipeline so the reranker sees everything.
	result, err := s.retrieveCandidates(ctx, req.Query, req)
	if err != nil {
		return nil, err
	}

	// Stage 2: optional explain — capture raw retrieval scores before
	// reranking rewrites Rank. Recency + metadata stages will fill in the
	// remaining ScoreDetails fields IF this is non-nil.
	if opts.IncludeScoreDetails {
		for i := range result.Documents {
			result.Documents[i].ScoreDetails = &model.ScoreDetails{
				Retrieval: result.Documents[i].Rank,
			}
		}
	}

	// Stage 3: rerank with Voyage rerank-2. `reranked` reports whether the
	// documents were ACTUALLY rescored — false when no reranker is wired,
	// when there's nothing to rerank (<=1 candidate), or when the reranker
	// call failed and we kept the original retrieval order. The trust weights
	// and score floor must act only on real reranker scores; applying the
	// floor to raw RRF scores (bounded ~0.033) would wipe every result — see
	// the project rule against filtering on RRF scores.
	result, reranked := s.rerankResults(ctx, req.Query, result)

	if opts.IncludeScoreDetails {
		for i := range result.Documents {
			if result.Documents[i].ScoreDetails != nil {
				result.Documents[i].ScoreDetails.Reranker = result.Documents[i].Rank
			}
		}
	}

	// Pull a snapshot of ranking config once per query.
	rankCfg := s.rankingConfig()

	applySourceTrustWeights(result, rankCfg, reranked)
	applyRerankerFloor(result, rankCfg, reranked)

	// Stage 5: recency decay.
	search.ApplyRecencyDecay(result, rankCfg)

	// Stage 6: metadata bonus.
	if rankCfg.MetadataBonusEnabled {
		search.ApplyMetadataBonus(result, req.Query)
	}

	// Stage 6b: pinpoint match attribution for window hits.
	applyWindowMatches(result)

	return result, nil
}

func (s *SearchService) rankingConfig() search.RankingConfig {
	if s.ranking == nil {
		return search.DefaultRankingConfig()
	}
	return s.ranking.Get()
}

// retrieveCandidates fetches the candidate pool, preferring HybridSearch when
// an embedder is configured and the query successfully embeds. Falls back to
// BM25-only on any embed / hybrid error so a degraded embedding path never
// turns into a user-visible failure.
func (s *SearchService) retrieveCandidates(ctx context.Context, query string, req model.SearchRequest) (*model.SearchResult, error) {
	var result *model.SearchResult
	if embedder := s.em.Get(); embedder != nil {
		embeddings, err := embedder.Embed(ctx, []string{query}, embedding.InputTypeQuery)
		if err == nil && len(embeddings) > 0 {
			result, err = s.search.HybridSearch(ctx, req, embeddings[0])
			if err != nil {
				s.log.Warn("hybrid search failed, falling back to BM25", zap.Error(err))
				result = nil
			}
		}
	}
	if result != nil {
		return result, nil
	}
	return s.search.Search(ctx, req)
}

// rerankResults reorders Documents by reranker score. Drops near-duplicates
// before sending to the reranker so we don't burn API budget on multiple
// chunks of the same boilerplate-heavy newsletter. The returned bool reports
// whether the documents were actually rescored: false when no reranker is
// wired, when there's nothing to rerank (<=1 candidate, so the API call is
// skipped), or when the reranker call errored and we kept the original
// retrieval order. In every false case Documents still carry raw retrieval
// (RRF/BM25) scores, which downstream trust-weighting and the score floor
// must not treat as reranker scores.
func (s *SearchService) rerankResults(ctx context.Context, query string, result *model.SearchResult) (*model.SearchResult, bool) {
	reranker := s.rm.Get()
	if reranker == nil || len(result.Documents) <= 1 {
		return result, false
	}

	result.Documents = dedupeNearDuplicates(result.Documents)

	texts := make([]string, len(result.Documents))
	for i, doc := range result.Documents {
		texts[i] = doc.Title + " " + doc.Content
	}

	ranked, err := reranker.Rerank(ctx, query, texts)
	if err != nil {
		s.log.Warn("reranking failed, using original order", zap.Error(err))
		return result, false
	}

	return reorderByRerankScores(result, ranked), true
}
