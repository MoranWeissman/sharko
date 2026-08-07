import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist']),
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
      // Codebase convention: a leading underscore marks a parameter that
      // must exist to satisfy a callback's type signature but isn't used
      // by that particular call site (see src/views/Observability.tsx).
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],

      // documented-off: react-hooks/set-state-in-effect — 76 pre-existing
      // violations across 47 files, tracked for cleanup. This is
      // eslint-plugin-react-hooks v7's new (React Compiler-derived) rule
      // against calling setState synchronously in a useEffect body. It
      // fires on the "fetch on mount" / "poll on an interval" pattern used
      // throughout this codebase's data-loading components (see the
      // Operations polling pattern and Init Progress pattern documented in
      // .claude/team/frontend-expert.md) — a legitimate, intentional
      // architecture, not a bug. Rewriting all 47 files to the
      // derived-state style the rule wants is a real but separate
      // refactor, out of scope for turning lint on in CI.
      'react-hooks/set-state-in-effect': 'off',

      // documented-off: react-refresh/only-export-components — 75
      // pre-existing violations across 27 files, tracked for cleanup. This
      // rule is a Vite Fast Refresh dev-experience check (not a
      // correctness check): it flags any file that exports something
      // besides components (a context + its Provider + its hook, a
      // vendored shadcn/ui component + its cva variants, a page that also
      // exports a constant others import). All of those are established,
      // intentional patterns here (see the Hooks and shadcn/ui sections in
      // .claude/team/frontend-expert.md). Splitting 27 files apart just to
      // satisfy this is a structural change, out of scope here.
      'react-refresh/only-export-components': 'off',
    },
  },
])
