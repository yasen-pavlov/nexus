# Nexus web

React + TypeScript + Vite frontend for [Nexus](../README.md). Runs on
`localhost:5174` in dev with `/api` proxied to the Go backend on `:8080`.

## Stack

- React 19 + TypeScript + Vite
- TanStack Router (file-based) + Query + Table
- shadcn/ui (base-ui underneath) + Tailwind v4
- react-hook-form + zod
- Vitest + happy-dom + React Testing Library + MSW (component tests)
- Playwright (E2E)

## Commands

```bash
npm run dev          # Vite dev server
npm run build        # production build → dist/ (embedded into the Go binary)
npm run lint         # ESLint
npm test             # Vitest run
npm run test:watch   # Vitest watch
npm run test:e2e     # Playwright (auto-starts dev server)
npm run gen:types    # regenerate src/lib/api-schema.ts from docs/swagger.json
```

## Production build flow

The Go binary embeds `dist/` via `//go:embed` in
`internal/api/static/`. The Dockerfile builds the SPA in stage 1 and copies
`web/dist/` into the Go build context for stage 2.

Run `npm run gen:types` after any backend swagger annotation change so the
TypeScript types stay in sync.
