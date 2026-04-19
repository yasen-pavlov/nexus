import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist', 'coverage', '.v8-coverage']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // TanStack Router's file-based routing *requires* `export const Route =
      // createFileRoute(...)` next to the route component. Whitelist that one
      // export name; everything else still has to follow the rule.
      'react-refresh/only-export-components': [
        'error',
        { allowExportNames: ['Route'] },
      ],
    },
  },
  // Route files co-locate `Route` with unexported route components by design,
  // which react-refresh can't reconcile with Fast Refresh. The router handles
  // HMR itself, so turn the rule off for the routes directory and rely on the
  // per-file rule above everywhere else.
  {
    files: ['src/routes/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])
