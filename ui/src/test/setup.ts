import '@testing-library/jest-dom/vitest'
import { afterEach } from 'vitest'
import { clearAllCached } from '@/lib/viewCache'

// perf S2's view cache is a module-scoped Map (deliberately — it's a
// same-session cache, not persisted storage). Without this, whichever test
// runs first in a file would seed the cache and every later test in that
// file would paint from its stale data instead of its own mocks.
afterEach(() => {
  clearAllCached()
})
