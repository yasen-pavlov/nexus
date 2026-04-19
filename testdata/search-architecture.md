# Search Architecture

Notes on the hybrid search pipeline I want to build into Nexus.

## Retrieval

Two retrieval strategies run in parallel and are fused:

1. **BM25 full-text** over OpenSearch with per-language analyzers (bulgarian,
   english, german). Cheap, explainable, nails exact matches and proper nouns.
2. **Dense vector** over the same chunks. Each chunk is 500 tokens with ~100
   tokens of overlap; we embed each chunk using the configured provider
   (Voyage, OpenAI, Cohere, or local Ollama) and store the vector alongside
   the BM25 document.

Reciprocal rank fusion (RRF) merges the two ranked lists. RRF is purely a
rank-fusion function — its scores are not interpretable as relevance, so we
deliberately do not floor on RRF scores.

## Reranking

After fusion we take the top ~50 candidates and send them through a dedicated
reranker (Voyage `rerank-2` or Cohere `rerank-3`). The reranker returns a
calibrated relevance score that *is* meaningful, so this is where we apply a
floor (currently 0.12).

Before the reranker call we dedupe near-duplicates — chunks sharing the same
first 200 characters of title+content are collapsed to avoid wasting reranker
budget on multiple chunks of the same boilerplate-heavy newsletter.

## Source-aware boosting

Every document carries a `source` field (filesystem, imap, telegram,
paperless). The ranking layer applies:

- A **half-life** per source, so newer email outranks old email, but the
  filesystem has a much longer half-life since a 10-year-old design doc is
  often still the best match.
- A **recency floor** so documents older than N days do not get penalised
  further.
- A **trust weight** per source — Paperless (curated documents) gets the
  strongest weight, Telegram (chatter) the weakest.

All three constants live alongside the connector definition so adding a new
source requires a single change.
