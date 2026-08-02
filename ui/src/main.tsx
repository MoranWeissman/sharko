import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { handleChunkLoadFailure, clearChunkReloadFlag, isChunkLoadError } from '@/lib/chunkReloadGuard'

// sprint-backlog-burndown-r1 Lane B-a — self-heal a rolled-over tab.
//
// After a live image roll, an already-open tab can still hold the OLD
// index.html, which references chunk files the NEW image no longer ships.
// Vite fires `vite:preloadError` on window when a dynamic import / chunk
// preload fails; a lazy route's import() rejection can also surface as a
// plain unhandled promise rejection depending on where in React's
// lazy/Suspense machinery it escapes. Either way, one guarded reload (see
// chunkReloadGuard.ts) picks up the current shell. If the reload already
// happened this session and it's still failing, we don't reload again —
// that's a genuinely broken deploy, not a stale tab, and should surface
// normally instead of looping forever.
const reloadDeps = { storage: window.sessionStorage, reload: () => window.location.reload() }

window.addEventListener('vite:preloadError', () => {
  handleChunkLoadFailure(reloadDeps)
})

window.addEventListener('unhandledrejection', (event) => {
  if (isChunkLoadError(event.reason)) {
    handleChunkLoadFailure(reloadDeps)
  }
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

// The app booted successfully past this point — clear the guard flag so a
// future genuine chunk failure (e.g. the NEXT deploy) gets its own fresh
// reload attempt instead of being silently suppressed by a stale flag.
clearChunkReloadFlag(window.sessionStorage)
