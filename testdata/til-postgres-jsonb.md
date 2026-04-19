# TIL: Postgres JSONB indexing tradeoffs

I hit a surprise today. We were storing connector configuration as `jsonb` and
querying with `WHERE config @> '{"enabled": true}'`. Worked fine, but the
query plan showed a sequential scan despite a GIN index on the column.

## The gotcha

The default GIN operator class for `jsonb` is `jsonb_ops`, which supports
`@>`, `?`, `?&`, and `?|`. What I had created was `jsonb_path_ops`, which only
supports `@>` but is much smaller and faster for that single case.

I was querying with `@>`, so `jsonb_path_ops` *should* have worked. The reason
it did not: my predicate was `config->>'enabled' = 'true'`, not `config @>
'{"enabled": true}'`. The string extraction operator `->>`  bypasses GIN
entirely.

## Fix

Rewrote the query to use containment:

```sql
SELECT id FROM connector_configs WHERE config @> '{"enabled": true}';
```

Index kicked in, 120ms → 3ms on our prod-size dataset.

## Takeaway

`->>` is great for *retrieving* values but not for *filtering* when you have a
GIN index. For filter predicates on jsonb, write containment expressions.
