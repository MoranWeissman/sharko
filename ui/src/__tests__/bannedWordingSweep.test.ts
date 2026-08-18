// bannedWordingSweep — the TypeScript half of the wording ban that actually
// sweeps. The Go half is internal/api/banned_wording_sweep_test.go, whose own
// doc comment says `ui/` is deliberately out of its scope because "the
// TypeScript lists own that side". This file is that side.
//
// # Why a sweep, when there are already banned-phrase lists
//
// "…what Sharko intends" survived in five Go files, two swagger summaries,
// four CLI strings and a live server sentence while SEVEN separate
// banned-phrase tests were running. Every one of them banned a complete
// VERDICT SENTENCE inside ONE named file. A fragment travelling across files
// was invisible to all of them — including the two TypeScript lists, which
// banned the same two whole sentences.
//
// So this test does the opposite: it walks the whole `ui/src` tree and looks
// for the FRAGMENT, case-insensitively, reporting every hit with its file and
// line. It is deliberately blunt. A phrase the product owner has killed
// should be impossible to reintroduce anywhere, including in a comment —
// comments are where the next person learns the wrong vocabulary.
//
// # Line-wrapped hits count
//
// A comment can split a banned phrase across two lines with a `//` and
// leading spaces in between, which is exactly how one occurrence hid from a
// plain grep on the Go side. Comment markers and runs of whitespace are
// normalised to single spaces before matching, over a small sliding window,
// so a wrapped phrase is found too.

import { describe, it, expect } from 'vitest'

/**
 * Every TypeScript source file under ui/src, as raw text. Vite's own glob —
 * no node built-ins, so this compiles under the app tsconfig (which has no
 * @types/node) exactly like the rest of the suite. Keys are project-relative
 * paths, e.g. "/src/views/ManagedSecrets.tsx".
 */
const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', { eager: true, query: '?raw', import: 'default' }) as Record<string, string>

/**
 * Phrases the product owner has retired. Each says WHAT replaced it, because
 * a test that only says "no" teaches nobody what to write instead.
 */
const BANNED: { phrase: string; instead: string }[] = [
  {
    // Ruling (b), 2026-08-19. The locked model is "Git defines the
    // connection; Sharko resolves credential references and maintains the
    // rendered Secret" — so a difference is a difference from GIT, never
    // from an intention of Sharko's.
    phrase: 'sharko intends',
    instead:
      'write around "the Git-defined connection". Where a full sentence is needed, the ruled text is exactly: ' +
      '"At least one compared field differs from the Git-defined connection."',
  },
]

/**
 * Files whose JOB is to name a banned phrase. Each needs a reason here;
 * anything else is a hit.
 */
// This file itself needs no entry: import.meta.glob excludes the module that
// calls it, so the sweep never reads its own phrase list.
const EXEMPT: Record<string, string> = {
  '/src/views/__tests__/ManagedSecrets.panel.test.tsx': 'per-file banned-phrase list; naming the phrase is the assertion',
  '/src/views/__tests__/ConnectionReconciliationView.test.tsx': 'per-file banned-phrase list; naming the phrase is the assertion',
}

/** Collapses `//` comment markers and any run of whitespace to a single space. */
const COMMENT_AND_SPACE_RUN = /(?:\/\/+|\s)+/g

/** How many consecutive lines are normalised together before matching. */
const WRAP_WINDOW = 3

function hitsIn(text: string, phrase: string): number[] {
  const lines = text.split('\n')
  const found: number[] = []
  for (let i = 0; i < lines.length; i++) {
    const window = lines.slice(i, i + WRAP_WINDOW).join('\n')
    const normalised = window.replace(COMMENT_AND_SPACE_RUN, ' ').toLowerCase()
    if (normalised.includes(phrase)) {
      // Report the first line of the window, and skip past it so a wrapped
      // phrase is not reported once per line of its window.
      found.push(i + 1)
      i += WRAP_WINDOW - 1
    }
  }
  return found
}

describe('retired wording cannot come back anywhere under ui/src', () => {
  for (const banned of BANNED) {
    it(`"${banned.phrase}" appears in no source file`, () => {
      const offenders: string[] = []
      for (const [path, text] of Object.entries(SOURCES)) {
        if (EXEMPT[path]) continue
        for (const line of hitsIn(text, banned.phrase)) offenders.push(`${path}:${line}`)
      }
      expect(offenders, `banned wording "${banned.phrase}" found. Instead, ${banned.instead}\n  ${offenders.join('\n  ')}`).toEqual([])
    })
  }

  it('finds a phrase wrapped across two comment lines — the shape that hid from a plain grep', () => {
    const wrapped = ['// At least one compared field does not match what', '// Sharko intends.'].join('\n')
    expect(hitsIn(wrapped, 'sharko intends')).not.toEqual([])
  })

  it('actually reads the tree — a sweep over an empty file list would pass forever', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(50)
    expect(SOURCES['/src/views/ManagedSecrets.tsx']).toBeTruthy()
  })

  it('every exemption names a file that still exists — a stale exemption is a hole', () => {
    for (const path of Object.keys(EXEMPT)) {
      expect(SOURCES[path], `exempt file ${path} is gone — drop the exemption`).toBeTruthy()
    }
  })
})
