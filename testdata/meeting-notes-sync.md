# Weekly sync — 2026-W15

Attendees: A, B, C.

## Decisions

- Ship the Telegram attachment download on Thursday. Keep the feature flag on
  until we see the binary cache fill without hot spots in metrics.
- Roll the IMAP body-cleaning pipeline to everyone on Monday. The
  experiment group has been on for three weeks with a 22% reduction in
  irrelevant "newsletter" results.
- Pause the Paperless connector metadata sync feature. We keep finding edges
  where tag IDs don't round-trip cleanly across instances. Revisit in May.

## Follow-ups

- [ ] Dashboard for sync run durations, grouped by connector. Currently only
  individual runs are visible; the trend is what we actually need.
- [ ] Rate limit on the login endpoint to slow down credential-stuffing.
  Track the number of failed attempts per IP in memory for now — Redis later
  if we need persistence across restarts.
- [ ] Document the `SourceTrustWeight` constants for contributors. Nothing
  about them is obvious from the code alone.

## Notes

B showed a neat demo of using the reranker's top score as a confidence signal
in the UI — searches where nothing clears 0.3 get a "no great matches"
disclaimer instead of a long list of mediocre ones. Everyone liked it. On
the backlog.
