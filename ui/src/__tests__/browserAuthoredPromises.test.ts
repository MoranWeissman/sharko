// browserAuthoredPromises — the browser does not tell anyone what the server
// is going to do.
//
// # The rule, and why it needed a mechanism
//
// The product owner's fourth structural zero: "zero browser-authored promises
// about server behaviour." Four shipped sentences broke it, named line by
// line. Three of them were still shipping when the bounded final review went
// looking, and the review's finding was blunt: "Zero 4 has no enforcing
// mechanism at all." Three `not.toContain(...)` assertions pinned one retired
// phrase, and nothing at all stood between the tree and the next one.
//
// A promise about server behaviour is different from a wrong sentence. A wrong
// sentence can be corrected. A promise cannot be kept by the browser at all —
// it is a claim about code running somewhere else, made by code that cannot
// see it, and it stays a claim long after the server has changed its mind. Two
// of the four said the same thing a word apart, in the same file, because two
// people each wrote down what they believed the server did.
//
// # This file is a LIST in both directions, never a count
//
// Two lists, and each fails whether it grows OR goes stale:
//
//   RETIRED_PROMISES        the exact sentences that were withdrawn. Fails if
//                           one comes back anywhere under ui/src, AND fails if
//                           the thing that replaced it has left the tree —
//                           because an entry saying "we say X now" stops being
//                           true the moment X is gone.
//
//   UNRULED_BROWSER_CLAIMS  sentences the shape detector finds that the product
//                           owner has NOT ruled on. Each carries the surface it
//                           belongs to and why it is written down rather than
//                           removed. Fails on a hit nobody classified, and
//                           fails on an entry whose text is no longer there.
//
// # The detector is a SHAPE detector and says so
//
// No test can judge whether a sentence is honest. What it can do is find the
// SHAPE that produced every one of the four defects: Sharko named as the
// subject, and a word claiming what it will do or habitually does — "will",
// "never", "keeps", "maintains", "repairs", "automatically", "every <so
// often>". Everything it finds is then removed or written down. Its own
// detection is proved on planted input, so its silence means something.
//
// It reads shipped browser code only, and only on the connection surface —
// the list is right here in the open, the same way the Go side's
// connectionContractFiles is, because adding a file to it or leaving one off
// is a decision somebody should have to make deliberately.

import { describe, it, expect } from 'vitest'
import { stripComments, LITERAL } from './sourceScan'

/** Every TypeScript source file under ui/src, as raw text, keyed "/src/…". */
const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', { eager: true, query: '?raw', import: 'default' }) as Record<
  string,
  string
>

/**
 * The shipped browser files that render the connection surface. Test files
 * and the generated catalog are deliberately absent: a test may quote
 * anything, and the generated file holds the SERVER's sentences, which are
 * the server's to promise.
 *
 * This is a list and not a glob for the same reason the Go side keeps
 * connectionContractFiles by hand: putting a file in or leaving it out is a
 * real decision, and it should be visible as one. A missing file fails below
 * rather than being skipped, so the list cannot rot into a name for something
 * that moved.
 */
const PROMISE_SURFACE = [
  '/src/views/ConnectionReconciliationView.tsx',
  '/src/views/ManagedSecrets.tsx',
  '/src/views/connectionActivity.ts',
  '/src/views/connectionHealthWords.ts',
  '/src/views/ClustersOverview.tsx',
  '/src/components/ConnectionOwnerBadge.tsx',
  '/src/components/ClusterActionHints.ts',
  '/src/components/resource/StatusMark.tsx',
  '/src/hooks/useConnectionHealth.ts',
]

/**
 * The shape of a promise: Sharko as the subject, and a claim about what it
 * will do or habitually does.
 *
 * THE EM-DASH IS EXCLUDED FROM THE WINDOW ON PURPOSE. Sharko's copy uses an
 * em-dash to start a new clause, so allowing the match to run past one turns
 * "the fields Sharko reads from the cluster — every value here is a
 * placeholder, never the real one" into a false hit on a sentence that
 * promises nothing. Forty characters and no clause break is the span in which
 * a verb still belongs to "Sharko".
 *
 * "every <duration>" is in the list because a cadence is a promise even with
 * no verb of its own: "every 5 minutes" says the same thing "repeatedly" says.
 * That arm is what catches the pair of sentences whose only difference is
 * "checks" versus "re-checks" — a ban that knows one spelling is the failure
 * this round has hit repeatedly.
 */
const PROMISE_SHAPE =
  /\bSharko\b[^.!?—]{0,40}?\b(?:will|won't|never|always|keeps?|maintains?|repairs?|reconciles?|re-applies)\b|\bSharko\b[^.!?]{0,80}?\bautomatically\b|\bSharko\b[^.!?—]{0,40}?\bevery\s+(?:\$\{|\d|few\b)|\bit\s+never\s+(?:writes|touches|changes|deletes|rotates)\b/i

interface RetiredPromise {
  /** The exact sentence, as it shipped. */
  text: string
  /** What it promised that the browser had no way to know. */
  why: string
  /** Where the fact is stated now, so the entry expires when that moves. */
  replacement: { file: string; text: string }
}

/**
 * The promises the product owner named and withdrew.
 *
 * The `text` here is a written-out literal, not an import. That is the point:
 * an assertion that reads the same constant the code reads compares a thing
 * with itself and can never fail. These bytes are the pin.
 */
const RETIRED_PROMISES: RetiredPromise[] = [
  {
    text: 'Someone else created this one — Sharko will not touch it.',
    why:
      'The half after the dash is a claim about what the server will do, written in the browser. It was ' +
      'also one of two wordings of one fact in one file — the ruling was "both near-duplicates are ' +
      'removed, not one chosen", so this went with its twin below rather than one being picked.',
    replacement: {
      file: '/src/views/ManagedSecrets.tsx',
      text: 'This secret was created by something other than Sharko.',
    },
  },
  {
    text: 'Someone else created this secret — Sharko will not touch it.',
    why: 'The twin of the entry above, one word apart, in the same file. Two authors, one fact.',
    replacement: {
      file: '/src/views/ManagedSecrets.tsx',
      text: 'This secret was created by something other than Sharko.',
    },
  },
  {
    text: 'Sharko creates the ArgoCD cluster secret and keeps its credentials up to date.',
    why:
      '"keeps its credentials up to date" is ongoing work the form has no way to observe, and the ' +
      'sentence used the retired word "cluster secret" where the product says "connection". A hint under ' +
      'a radio button describes the choice, which is a fact about the form.',
    replacement: {
      file: '/src/views/ClustersOverview.tsx',
      text: "Hand this cluster&apos;s connection to Sharko. The usual choice.",
    },
  },
  {
    text: 'Sharko only manages the addon labels on it — it never writes, rotates, or deletes the credentials. ',
    why:
      'A "never" about the server, typed in the browser — and the server already had its own sentence for ' +
      'this exact mode, worded differently. The caption now renders modeStatementSelfManaged from the ' +
      'generated contract, so there is one author again.',
    replacement: {
      file: '/src/components/ConnectionOwnerBadge.tsx',
      text: 'CONNECTION_SENTENCES.modeStatementSelfManaged',
    },
  },
]

interface UnruledClaim {
  file: string
  text: string
  why: string
}

/**
 * Sentences the shape detector finds that the product owner has NOT ruled on.
 *
 * WHAT AN ENTRY HERE MEANS, precisely, because it would be easy to read it as
 * an excuse: it means this sentence has the shape of a browser-authored claim
 * about server behaviour, it is outside the connection ruling's scope, and it
 * is recorded so that it is visible and so that a NEW one cannot arrive
 * unnoticed. It does not mean somebody decided it is fine. Nothing here is
 * hidden, and the list expires: an entry whose text has left the file fails.
 */
const UNRULED_BROWSER_CLAIMS: UnruledClaim[] = [
  {
    file: '/src/views/ManagedSecrets.tsx',
    text: 'Git-defined cluster connections Sharko maintains for Argo CD.',
    why:
      'The page subtitle, and the product owner wrote this exact replacement himself under ruling B5 — it ' +
      'is his sentence, not one the browser invented, and it states the locked model (Git defines the ' +
      'connection, Sharko maintains the resulting Secret) that the whole surface is built on.',
  },
  {
    file: '/src/views/ManagedSecrets.tsx',
    text: 'Sharko re-checks it every ${human}, and right after each merge.',
    why:
      "The cadence comes from the server: `${human}` is the check interval the response carries, so the " +
      'number is not invented here. The words around it are the browser\'s, and the addon-values half of ' +
      'this page is outside the connection ruling. Recorded, not removed.',
  },
  {
    file: '/src/views/ManagedSecrets.tsx',
    text: 'Sharko checks it every ${human} and repairs it automatically.',
    why:
      'The addon-values sibling of the sentence above, same server-supplied interval, same surface outside ' +
      'the connection ruling. It is here rather than merged with its sibling because they really do ' +
      'describe two different engines.',
  },
  {
    file: '/src/views/ManagedSecrets.tsx',
    text:
      'Sharko keeps these secrets in sync automatically. Git defines what should exist. Values come from your secret store.',
    why:
      'The subtitle of the legacy unified mode, which the file\'s own comment records as test-only and ' +
      'unreachable from the app — every route reaches one of the two split areas, which have their own ' +
      'subtitles. Kept so the legacy render path is not silently changed under its tests.',
  },
]

// ─────────────────────────────────────────────────────────────────────────────

interface Hit {
  file: string
  text: string
}

export function promiseHits(sources: Record<string, string>, files: readonly string[]): Hit[] {
  const hits: Hit[] = []
  const seen = new Set<string>()
  for (const file of files) {
    const raw = sources[file]
    if (raw === undefined) continue
    const code = stripComments(raw)
    for (const m of code.matchAll(LITERAL)) {
      const text = m[1] ?? m[2] ?? m[3]
      if (!PROMISE_SHAPE.test(text)) continue
      const key = `${file}${text}`
      if (seen.has(key)) continue
      seen.add(key)
      hits.push({ file, text })
    }
  }
  return hits
}

/** Every file whose CODE (comments removed) still contains `text`. */
function filesContaining(text: string): string[] {
  const out: string[] = []
  for (const [file, raw] of Object.entries(SOURCES)) {
    if (stripComments(raw).includes(text)) out.push(file)
  }
  return out
}

describe('the shape detector works before anything is concluded from its silence', () => {
  it('reads the tree, and every file on the connection surface is really there', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(50)
    const missing = PROMISE_SURFACE.filter((f) => SOURCES[f] === undefined)
    expect(
      missing,
      'A file on the connection surface list does not exist. It was renamed or moved, and this guard has ' +
        'been sweeping nothing for it ever since. Fix the path or drop the entry:\n  ' + missing.join('\n  '),
    ).toEqual([])
  })

  it('fires on every sentence that was withdrawn — otherwise its silence proves nothing', () => {
    for (const p of RETIRED_PROMISES) {
      expect(PROMISE_SHAPE.test(p.text), `the detector cannot see ${JSON.stringify(p.text)}`).toBe(true)
    }
  })

  it('fires on each promise word in turn, planted', () => {
    const planted = [
      "const a = 'Sharko will rebuild it for you.'",
      "const a = 'Sharko never deletes a secret it did not write.'",
      "const a = 'Sharko keeps the labels in step with Git.'",
      "const a = 'Sharko repairs it on its own schedule.'",
      'const a = `Sharko checks every ${n} minutes.`',
      "const a = 'Sharko sorts this out automatically once the merge lands.'",
    ]
    for (const src of planted) {
      expect(promiseHits({ '/src/p.ts': src }, ['/src/p.ts']), src).toHaveLength(1)
    }
  })

  it('does not fire on a sentence that only reports what happened, or on a new clause after a dash', () => {
    expect(
      promiseHits({ '/src/p.ts': "const a = 'Sharko could not read this connection just now.'" }, ['/src/p.ts']),
    ).toEqual([])
    expect(
      promiseHits(
        {
          '/src/p.ts':
            "const a = 'This shows only the fields Sharko reads from the cluster — every value here is a placeholder, never the real one.'",
        },
        ['/src/p.ts'],
      ),
    ).toEqual([])
  })

  it('reads a promise typed in backticks as readily as one in quotes', () => {
    expect(promiseHits({ '/src/p.ts': 'const a = `Sharko will rebuild it for you.`' }, ['/src/p.ts'])).toHaveLength(1)
  })

  it('ignores a promise written inside a comment — a comment explaining what was removed is prose', () => {
    expect(
      promiseHits({ '/src/p.ts': "// it used to say 'Sharko will rebuild it for you.'\nconst a = 1" }, ['/src/p.ts']),
    ).toEqual([])
  })
})

describe('no withdrawn promise has come back', () => {
  it('none of them appears in browser code anywhere under ui/src', () => {
    const back: string[] = []
    for (const p of RETIRED_PROMISES) {
      for (const file of filesContaining(p.text)) back.push(`${file}  ${JSON.stringify(p.text)}`)
    }
    expect(
      back,
      'A sentence the product owner withdrew is being said again. It is a promise about what the server ' +
        'will do, written where the server cannot see it. Say the fact the browser actually has, or render ' +
        "the server's own sentence from @/generated/connection-sentences.\n  " + back.join('\n  '),
    ).toEqual([])
  })

  it('what replaced each one is still in the tree — an entry that names nothing is a hole', () => {
    const stale: string[] = []
    for (const p of RETIRED_PROMISES) {
      const raw = SOURCES[p.replacement.file]
      if (raw === undefined) {
        stale.push(`${p.replacement.file} is gone (named as the replacement for ${JSON.stringify(p.text)})`)
        continue
      }
      if (!raw.includes(p.replacement.text)) {
        stale.push(
          `${p.replacement.file} no longer contains ${JSON.stringify(p.replacement.text)}, ` +
            `which this list says replaced ${JSON.stringify(p.text)}`,
        )
      }
    }
    expect(
      stale,
      'This list says "we say X instead now", and X is no longer there. Either the replacement moved and ' +
        'the entry needs updating, or the sentence lost its replacement altogether and somebody should ' +
        'look at what the surface says now.\n  ' + stale.join('\n  '),
    ).toEqual([])
  })
})

describe('every remaining claim on the connection surface is written down', () => {
  const HITS = promiseHits(SOURCES, PROMISE_SURFACE)

  it('nothing on the surface promises server behaviour without an entry saying so', () => {
    const classified = new Set(UNRULED_BROWSER_CLAIMS.map((c) => `${c.file}${c.text}`))
    const unclassified = HITS.filter((h) => !classified.has(`${h.file}${h.text}`)).map(
      (h) => `${h.file}  ${JSON.stringify(h.text)}`,
    )
    expect(
      unclassified,
      'A browser sentence claims what Sharko will do or habitually does. The browser cannot know that. ' +
        "Render the server's sentence from @/generated/connection-sentences, or say only the fact this " +
        'browser was actually given. If it genuinely belongs to a surface outside the connection ruling, ' +
        'add it to UNRULED_BROWSER_CLAIMS with the surface and the reason.\n  ' + unclassified.join('\n  '),
    ).toEqual([])
  })

  it('every entry still names text that is really there — a stale entry switches the guard off quietly', () => {
    const present = new Set(HITS.map((h) => `${h.file}${h.text}`))
    const stale = UNRULED_BROWSER_CLAIMS.filter((c) => !present.has(`${c.file}${c.text}`)).map(
      (c) => `${c.file}  ${JSON.stringify(c.text)}`,
    )
    expect(
      stale,
      'An entry names a sentence that is not in that file any more. Drop it — a list that keeps entries ' +
        'nobody can find stops being a list of anything.\n  ' + stale.join('\n  '),
    ).toEqual([])
  })

  it('every entry says WHY, at length enough to be a reason', () => {
    for (const c of UNRULED_BROWSER_CLAIMS) {
      expect(c.why.length, `${c.file}: ${JSON.stringify(c.text)} has no reason written down`).toBeGreaterThan(80)
    }
  })
})
