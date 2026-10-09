// serverSentenceCopies — no server-authored sentence may be typed into
// browser code again.
//
// # What went wrong, and why a guard rather than a habit
//
// Fifty-two sentences the server authors had been typed by hand into
// twenty-one browser files. Nothing compared a copy with its original, so a
// copy went stale the moment somebody reworded the Go constant, and no test
// went red — the fixture and the assertion read the same browser constant, so
// they agreed with each other while both described a response the server
// cannot send. Three were still stale when story P5 converted them: they said
// "what git defines" where the server says "Git".
//
// The product owner's ruling is that the browser renders the completed
// sentence it received and does not reproduce the server's wording. Story P5
// carried that out. This test is what stops it coming back.
//
// # How it works
//
// It reads every TypeScript file under ui/src (Vite's own glob, no node
// built-ins, same as bannedWordingSweep), strips comments, pulls out every
// string literal, and compares each one against the generated catalogs —
// CONNECTION_SENTENCES and CONNECTION_FAILURE_MESSAGES, both written by
// cmd/gen-connection-sentences from the Go source of truth.
//
// It flags two things, not one:
//
//   EXACT — the literal IS a server sentence. A fresh copy.
//   NEAR  — the literal is nearly a server sentence. A copy that has already
//           drifted, which is the shape that hides: it matches no grep for
//           the current wording, so a guard looking only for exact text would
//           report a clean tree while the stale copy sat there.
//
// # It is a list, not a count
//
// A count passes while its entries rot — that failure mode has already bitten
// this repository (a hidden `families < 40` floor). So every permitted hit is
// named in BROWSER_OWNED_COPY with the surface that owns it, and this test
// fails in BOTH directions:
//
//   - a hit in the tree with no entry            → a copy nobody classified
//   - an entry naming text that is not there     → a stale entry, a hole
//   - an entry whose collidesWith no longer
//     holds that text                            → the catalog moved under it
//
// The third one matters as much as the first two: an entry is a claim that
// two owners agree on a word, and that claim expires when either side changes.

import { describe, it, expect } from 'vitest'
import { CONNECTION_SENTENCES, CONNECTION_FAILURE_MESSAGES } from '@/generated/connection-sentences'
import { BROWSER_OWNED_COPY } from './browserOwnedCopy'
import { stripComments, LITERAL } from './sourceScan'

/** Every TypeScript source file under ui/src, as raw text, keyed "/src/…". */
const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', { eager: true, query: '?raw', import: 'default' }) as Record<
  string,
  string
>

/** The generated file holds the catalog itself; reading it would flag all 130. */
const GENERATED = '/src/generated/connection-sentences.ts'

/**
 * The classification list quotes each collision by exact text — that is its
 * job, the same way bannedWordingSweep's phrase list has to name the phrase.
 * Skipping it is not a hole: the "stale entry" check below looks an entry's
 * text up in the hits found OUTSIDE this file, so an entry whose text exists
 * only here fails as stale. There is nowhere to hide a copy.
 */
const CLASSIFICATIONS = '/src/__tests__/browserOwnedCopy.ts'

/**
 * Both catalogs, id → sentence. The failure family is included deliberately:
 * its fifty finished sentences are exactly the ones the browser used to
 * assemble from fragments, and one hand-typed fallback in useConnectionHealth
 * had already drifted (it carried a full stop the server never emits).
 */
const CATALOG: Record<string, string> = { ...CONNECTION_SENTENCES, ...CONNECTION_FAILURE_MESSAGES }

/**
 * Sentence → the ids that hold it. Two ids CAN share one sentence: the vault
 * and secrets kinds of the failure family produce identical text, so reverse
 * lookup is genuinely ambiguous there and the guard reports all matching ids
 * rather than silently picking one.
 */
const BY_SENTENCE = new Map<string, string[]>()
for (const [id, sentence] of Object.entries(CATALOG)) {
  const ids = BY_SENTENCE.get(sentence)
  if (ids) ids.push(id)
  else BY_SENTENCE.set(sentence, [id])
}

/**
 * Below this length a "sentence" is a single common word and the near-miss
 * comparison stops meaning anything. Exact matching still applies at any
 * length — the shortest catalog entry is "Blocked", seven characters with no
 * space in it, and a filter built on "a sentence contains a space" would have
 * skipped it silently.
 */
const MIN_NEAR_LENGTH = 18

/** Dice coefficient over character bigrams. 1 = identical, 0 = nothing shared. */
function similarity(a: string, b: string): number {
  if (a === b) return 1
  if (a.length < 2 || b.length < 2) return 0
  const bigrams = new Map<string, number>()
  for (let i = 0; i < a.length - 1; i++) {
    const g = a.slice(i, i + 2)
    bigrams.set(g, (bigrams.get(g) ?? 0) + 1)
  }
  let shared = 0
  for (let i = 0; i < b.length - 1; i++) {
    const g = b.slice(i, i + 2)
    const n = bigrams.get(g) ?? 0
    if (n > 0) {
      bigrams.set(g, n - 1)
      shared++
    }
  }
  return (2 * shared) / (a.length - 1 + (b.length - 1))
}

/** How close a literal has to be before it reads as a drifted copy. */
const NEAR_THRESHOLD = 0.92

interface Hit {
  file: string
  text: string
  ids: string[]
  kind: 'EXACT' | 'NEAR'
}

export function findHits(sources: Record<string, string>): Hit[] {
  const hits: Hit[] = []
  const seen = new Set<string>()
  for (const [file, raw] of Object.entries(sources)) {
    if (file === GENERATED || file === CLASSIFICATIONS) continue
    const code = stripComments(raw)
    for (const m of code.matchAll(LITERAL)) {
      const text = (m[1] ?? m[2] ?? m[3]).replace(/\\'/g, "'").replace(/\\"/g, '"').replace(/\\`/g, '`')
      let ids = BY_SENTENCE.get(text)
      let kind: Hit['kind'] = 'EXACT'
      if (!ids) {
        if (text.length < MIN_NEAR_LENGTH) continue
        const near: string[] = []
        for (const [sentence, sentenceIds] of BY_SENTENCE) {
          if (sentence.length < MIN_NEAR_LENGTH) continue
          if (similarity(text, sentence) >= NEAR_THRESHOLD) near.push(...sentenceIds)
        }
        if (near.length === 0) continue
        ids = near
        kind = 'NEAR'
      }
      const key = `${file}\u001f${text}`
      if (seen.has(key)) continue
      seen.add(key)
      hits.push({ file, text, ids, kind })
    }
  }
  return hits
}

const HITS = findHits(SOURCES)

/**
 * The separator that joins a file path to a sentence to make one map key.
 *
 * It is written as the ESCAPE `\u001f`, not as the raw byte, and it is NOT a
 * NUL. Both of those matter and neither is style. This file used to join its
 * keys with two raw NUL bytes, and git decides a file is binary by looking for
 * a NUL in its first 8000 bytes — so all 382 lines of this guard entered the
 * repository as `Bin 0 -> 17092 bytes`, with not one reviewable line, and every
 * future change to it would have reached a reviewer the same way. A guard whose
 * own edits cannot be read is a guard nobody can check.
 *
 * Unit separator is the right character for the job (it can appear in neither a
 * file path nor a sentence), and spelling it as an escape keeps this file plain
 * ASCII, so the source reads as easily as the diff.
 */
function entryKey(file: string, text: string) {
  return `${file}\u001f${text}`
}

describe('no server-authored sentence is typed into browser code', () => {
  it('reads the tree and the catalogs — a sweep over nothing would pass forever', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(50)
    expect(SOURCES[GENERATED], 'the generated catalog file is missing from ui/src').toBeTruthy()
    expect(Object.keys(CATALOG).length).toBeGreaterThan(100)
    expect(BY_SENTENCE.size).toBeGreaterThan(100)
  })

  it('finds an exact copy — the guard would be worthless if it did not', () => {
    const planted = { '/src/planted.ts': `export const X = ${JSON.stringify(CONNECTION_SENTENCES.condLiveSecretFound)}` }
    const found = findHits(planted)
    expect(found).toHaveLength(1)
    expect(found[0].kind).toBe('EXACT')
    expect(found[0].ids).toContain('condLiveSecretFound')
  })

  it('finds a copy typed inside BACKTICKS — the hole two reviewers walked through', () => {
    // A template literal with no substitution in it is an ordinary string.
    // The pattern used to know only ' and ", so this exact paste passed the
    // whole guard in silence while the single-quoted version of the same
    // paste failed it. Planted, never mutated in the tree.
    const sentence = CONNECTION_SENTENCES.condLiveSecretFound
    const found = findHits({ '/src/planted.ts': 'export const X = `' + sentence + '`' })
    expect(found).toHaveLength(1)
    expect(found[0].kind).toBe('EXACT')
    expect(found[0].ids).toContain('condLiveSecretFound')

    // And the same sentence in each of the other two quote characters, so
    // this test names all three rather than trusting the two that worked.
    for (const q of ["'", '"']) {
      const alt = findHits({ '/src/planted.ts': `export const X = ${q}${sentence}${q}` })
      expect(alt, `a copy in ${q} quotes was not seen`).toHaveLength(1)
    }
  })

  it('finds a DRIFTED copy inside backticks too — near-matching is not quote-aware either', () => {
    const drifted = CONNECTION_SENTENCES.reasonOutOfSyncApprovalRequired.replace('Git defines', 'git defines')
    expect(BY_SENTENCE.has(drifted)).toBe(false)
    const found = findHits({ '/src/planted.ts': 'const x = `' + drifted + '`' })
    expect(found).toHaveLength(1)
    expect(found[0].kind).toBe('NEAR')
    expect(found[0].ids).toContain('reasonOutOfSyncApprovalRequired')
  })

  it('does not flag an ordinary template literal that carries a substitution', () => {
    expect(findHits({ '/src/planted.ts': 'const x = `Loading ${name} from the cluster…`' })).toEqual([])
  })

  it('finds a copy that has ALREADY drifted — the shape a grep for current wording misses', () => {
    // The real defect: three fixtures said "what git defines" where the
    // server says "Git". One changed letter, invisible to exact matching.
    const drifted = CONNECTION_SENTENCES.reasonOutOfSyncApprovalRequired.replace('Git defines', 'git defines')
    expect(drifted).not.toBe(CONNECTION_SENTENCES.reasonOutOfSyncApprovalRequired)
    expect(BY_SENTENCE.has(drifted)).toBe(false)
    const found = findHits({ '/src/planted.ts': `export const X = ${JSON.stringify(drifted)}` })
    expect(found).toHaveLength(1)
    expect(found[0].kind).toBe('NEAR')
    expect(found[0].ids).toContain('reasonOutOfSyncApprovalRequired')
  })

  it('finds the SHORTEST catalog entry — one word, no space, the case a space-based filter drops', () => {
    // Seven characters, no space in it. The neighbouring Go guard's
    // "a sentence contains a space" filter would have skipped it silently,
    // so this test proves length and spacing are not what selects a hit.
    expect(CONNECTION_SENTENCES.headlineBlocked.length).toBeLessThan(MIN_NEAR_LENGTH)
    expect(CONNECTION_SENTENCES.headlineBlocked).not.toContain(' ')
    const found = findHits({ '/src/planted.ts': `const x = ${JSON.stringify(CONNECTION_SENTENCES.headlineBlocked)}` })
    expect(found.map((h) => h.ids).flat()).toContain('headlineBlocked')
  })

  it('does not flag ordinary browser copy', () => {
    expect(findHits({ '/src/planted.ts': "const x = 'Search by cluster, addon, secret name, or namespace...'" })).toEqual([])
  })

  it('ignores comments — a quotation explaining what a change replaced is prose, not behaviour', () => {
    const commented = `// ${CONNECTION_SENTENCES.condLiveSecretFound}\n/* ${CONNECTION_SENTENCES.condLiveSecretFound} */\nexport const X = 1`
    expect(findHits({ '/src/planted.ts': commented })).toEqual([])
  })

  it('every hit in the tree is a classified browser-owned collision — a new copy fails here', () => {
    const classified = new Set(BROWSER_OWNED_COPY.map((e) => entryKey(e.file, e.text)))
    const unclassified = HITS.filter((h) => !classified.has(entryKey(h.file, h.text))).map(
      (h) => `${h.file}  ${h.kind}  ${h.ids.join('|')}  ${JSON.stringify(h.text)}`,
    )
    expect(
      unclassified,
      'A server-authored sentence is typed into browser code. Render what the server sent, or — where there is ' +
        'no server response on that path — import the value from @/generated/connection-sentences. If the word is ' +
        "genuinely the browser's own and only happens to match, classify it in ui/src/__tests__/browserOwnedCopy.ts " +
        'with the surface that owns it.\n  ' +
        unclassified.join('\n  '),
    ).toEqual([])
  })

  it('every classification still names text that is really there — a stale entry is a hole', () => {
    const present = new Set(HITS.map((h) => entryKey(h.file, h.text)))
    const stale = BROWSER_OWNED_COPY.filter((e) => !present.has(entryKey(e.file, e.text))).map(
      (e) => `${e.file}  ${JSON.stringify(e.text)}`,
    )
    expect(
      stale,
      'A browser-owned-copy entry names text that no longer appears in that file. Drop the entry — a list that ' +
        'keeps entries nobody can find stops being a list of anything.\n  ' +
        stale.join('\n  '),
    ).toEqual([])
  })

  it('every classification still collides with the sentence it claims — the catalog can move underneath it', () => {
    const broken: string[] = []
    for (const e of BROWSER_OWNED_COPY) {
      const sentence = CATALOG[e.collidesWith]
      if (sentence === undefined) {
        broken.push(`${e.file}: collidesWith "${e.collidesWith}" is in neither catalog`)
        continue
      }
      if (sentence !== e.text && similarity(e.text, sentence) < NEAR_THRESHOLD) {
        broken.push(
          `${e.file}: ${JSON.stringify(e.text)} no longer collides with ${e.collidesWith} = ${JSON.stringify(sentence)}`,
        )
      }
    }
    expect(
      broken,
      'A classification says two owners agree on a word, and they no longer do. Re-read the entry: either the ' +
        'collision is gone and the entry should go with it, or the browser word drifted and needs its own decision.' +
        '\n  ' +
        broken.join('\n  '),
    ).toEqual([])
  })

  it('every classification says WHY, and names a file that exists', () => {
    for (const e of BROWSER_OWNED_COPY) {
      expect(SOURCES[e.file], `classified file ${e.file} is gone`).toBeTruthy()
      expect(e.why.length, `${e.file}: "${e.text}" has no reason written down`).toBeGreaterThan(80)
    }
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The second half of the same rule: text a person reads is not an identifier.
//
// The generated notification-codes file says it in its own header — a title
// "may be reworded in any release. Never route on them, never use one as a
// map key, and never compare one." The System page was doing all three:
// it held two hand-typed copies of the poller's titles, compared each
// notification's title against them, and used the title as the map key. One
// capitalised letter server-side would have blanked both arrows' detail lines
// with nothing failing anywhere.
//
// The catalog guard above cannot see this class: notification titles are
// deliberately NOT generated (story P2b generated the codes and only the
// codes, precisely so nobody confuses the two), so there is no list of title
// text to compare a literal against. What CAN be checked is the shape — text
// being compared, searched, or used as an index. That is the thing that is
// always wrong, whatever the words are.
//
// # Why this used to look only at `.title`, and why that was not enough
//
// The product owner's ruling names three fields, not one: "title, reason or
// message". A guard that knew only `.title` reported a clean tree while three
// shipped surfaces decided what to render by reading a server sentence:
//
//   - the setup wizard chose between "this application already exists but is
//     not healthy" and "Sharko could not check" by asking whether the
//     server's diagnostic contained the substring "sync=";
//   - the cluster page chose between the "not found" empty state and the
//     error state by lower-casing the server's error sentence and looking in
//     it for "not found", "404" and "cluster not found";
//   - the adopt dialog decided whether an unverifiable cluster stayed
//     adoptable by hunting five phrases in the server's error_message — and
//     once a credentials failure started carrying one fixed sentence, that
//     hunt matched nothing and the contract silently died.
//
// None of the three was a `.title` comparison. All three are the same defect.
// So the guard now looks for three shapes, and the third one is the one that
// would have caught all three of those on its own.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Fields that are ALWAYS prose — words written for a person to read, with no
 * second life as an identifier. Comparing one, searching inside one, or using
 * one as a map key is wrong whatever the words happen to be.
 */
const PROSE_FIELDS = '(?:title|detail|description|headline|qualifier)'

/**
 * Fields that are prose on SOME responses and a closed set of codes on
 * others. `repo.reason` carries wire values like `no_connection`, and
 * `x.message` is a JavaScript Error's own field, so comparing one is not by
 * itself a finding.
 *
 * Searching INSIDE one still is: nobody substring-searches an identifier.
 * That asymmetry is why these two fields have their own list.
 */
const SOMETIMES_CODE_FIELDS = '(?:reason|message)'

/** `x.title === …`, `map[x.title]` — comparing or indexing by prose. */
const PROSE_ROUTING = new RegExp(
  `\\.${PROSE_FIELDS}\\s*(?:===|!==|==)|(?<=[\\w$)\\]])\\[[A-Za-z_$][\\w$]*\\.${PROSE_FIELDS}\\]`,
  'g',
)

/** `x.title.includes(…)`, `x.reason.startsWith(…)` — searching inside text. */
const TEXT_FIELD_SEARCH = new RegExp(
  `\\.(?:${PROSE_FIELDS}|${SOMETIMES_CODE_FIELDS})\\.(?:includes|startsWith|endsWith|match|indexOf|search)\\s*\\(`,
  'g',
)

/**
 * A substring search for a PHRASE — `.includes('not found')` — whatever the
 * thing being searched is called.
 *
 * THIS ARM IS THE ONE THAT EARNS ITS KEEP. The two arms above know field
 * names, and not one of the three shipped defects went through a field name:
 * each had already copied the sentence into a local (`const lower =
 * msg.toLowerCase()`, `const lower = error.toLowerCase()`) before searching
 * it. What they all have in common is on the OTHER side of the call — a
 * literal with a space in it, which is a phrase and not an identifier. Wire
 * values are `no_connection` and `in_sync`; they do not contain spaces.
 *
 * A space is the whole test, with no minimum length: "## " is a phrase this
 * guard should make somebody justify, and it is four characters long.
 */
const PHRASE_SEARCH =
  /\.(?:includes|startsWith|endsWith|indexOf|search)\(\s*(?:'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"|`((?:[^`\\]|\\.)*)`)/g

/**
 * A file this guard permits to search for phrases, the exact phrases it may
 * search for, and why.
 *
 * PER-PHRASE, NOT PER-FILE. An entry that just named the file would let the
 * next phrase in that file arrive unseen, which is the "a count passes while
 * its entries rot" failure this whole test file exists because of. Listing
 * the phrases means an eighteenth one in PullRequestsPanel fails here, and a
 * phrase that has left the file fails here too.
 */
interface PhraseSearchException {
  file: string
  why: string
  phrases: string[]
}

const PHRASE_SEARCH_EXCEPTIONS: PhraseSearchException[] = [
  {
    file: '/src/components/MarkdownRenderer.tsx',
    why:
      'Parsing Markdown, which is a FORMAT and not a message. "### " is punctuation in a grammar the ' +
      'browser is reading, not a sentence anybody wrote for a person, and there is no typed field a ' +
      'markdown parser could route on instead — the structure it is recovering is the text itself.',
    phrases: ['### ', '## ', '# ', '- ', '* '],
  },
  {
    file: '/src/components/PullRequestsPanel.tsx',
    why:
      'Bucketing a merged pull request into a nav section by its title. RECORDED, NOT FIXED, and it is a ' +
      'real instance of this class: Sharko authors these titles, so the browser is reading server text to ' +
      'decide what to render. The honest fix is a typed operation on the PR record — the endpoint does ' +
      "return an `operation` field, but internal/api/prs.go fills it with the title's FIRST WORD, which " +
      'is the commit prefix ("sharko:") on nearly every Sharko PR, so it cannot bucket anything. Making ' +
      'it usable is a server change outside this story. What is at stake meanwhile is which section a ' +
      'merged PR is filed under, not any claim about success, health or completion — so it is written ' +
      'down here where it is visible and cannot quietly grow, rather than left for the next sweep to ' +
      'rediscover.',
    phrases: [
      'initialize repository',
      'unadopt cluster',
      'adopt cluster',
      'register cluster',
      'remove cluster',
      'update addons for cluster',
      'add addon',
      'remove addon',
      'enable addon',
      'disable addon',
      'configure addon',
      'upgrade addon',
      'overrides on cluster',
      'global values for',
      'ai annotate',
      'ai opt-out',
      'to the catalog',
    ],
  },
  {
    file: '/src/views/AddonDetail.tsx',
    why:
      "The phrase being searched for is the BROWSER'S OWN. `labelTooltip` is handed a label this file " +
      'built a few lines earlier (`Latest in ${currentMajor}.x`), so both the text and the search belong ' +
      'to one author and there is no second party whose wording can move underneath it. Nothing here ' +
      'came off the wire.',
    phrases: ['Latest in '],
  },
]

/**
 * Files allowed to compare or index by prose, each with a reason. Empty, and
 * it should stay that way: there is no honest reason to branch on display
 * text.
 */
const PROSE_ROUTING_EXEMPT: Record<string, string> = {}

/** Shipped browser code only. A test may quote and compare anything it likes. */
function isShippedSource(file: string): boolean {
  return !file.includes('/__tests__/') && !/\.test\.tsx?$/.test(file)
}

export interface RoutingHit {
  file: string
  /** What was found, trimmed — the routing shape, or the phrase searched for. */
  text: string
  kind: 'prose-routing' | 'text-field-search' | 'phrase-search'
}

export function displayTextRoutingHits(sources: Record<string, string>): RoutingHit[] {
  const out: RoutingHit[] = []
  for (const [file, raw] of Object.entries(sources)) {
    if (!isShippedSource(file)) continue
    const code = stripComments(raw)
    if (!PROSE_ROUTING_EXEMPT[file]) {
      for (const m of code.matchAll(PROSE_ROUTING)) out.push({ file, text: m[0].trim(), kind: 'prose-routing' })
      for (const m of code.matchAll(TEXT_FIELD_SEARCH)) out.push({ file, text: m[0].trim(), kind: 'text-field-search' })
    }
    for (const m of code.matchAll(PHRASE_SEARCH)) {
      const phrase = m[1] ?? m[2] ?? m[3]
      if (!phrase.includes(' ')) continue
      out.push({ file, text: phrase, kind: 'phrase-search' })
    }
  }
  return out
}

describe('display text is never an identifier — not a title, not a reason, not a message', () => {
  const HITS = displayTextRoutingHits(SOURCES)

  it('nothing under ui/src compares prose or keys a map by it', () => {
    const bad = HITS.filter((h) => h.kind === 'prose-routing').map((h) => `${h.file}  ${h.text}`)
    expect(
      bad,
      'A sentence a person reads is being used to decide something. Route on a typed field instead — a ' +
        "notification's `code` (the closed set is in ui/src/generated/notification-codes.ts), an HTTP " +
        'status, or a state enum. Words get reworded; typed facts do not.\n  ' + bad.join('\n  '),
    ).toEqual([])
  })

  it('nothing searches inside a title, reason, detail, description, headline or message', () => {
    const bad = HITS.filter((h) => h.kind === 'text-field-search').map((h) => `${h.file}  ${h.text}`)
    expect(
      bad,
      'Something is looking for a substring inside text the server wrote. That is the shape the setup ' +
        'wizard, the cluster page and the adopt dialog each shipped, and all three broke the moment the ' +
        'server reworded a sentence. Ask a typed field.\n  ' + bad.join('\n  '),
    ).toEqual([])
  })

  it('every phrase searched for in shipped code is written down with a reason', () => {
    const allowed = new Set(PHRASE_SEARCH_EXCEPTIONS.flatMap((e) => e.phrases.map((p) => `${e.file} ${p}`)))
    const bad = HITS.filter((h) => h.kind === 'phrase-search')
      .filter((h) => !allowed.has(`${h.file} ${h.text}`))
      .map((h) => `${h.file}  ${JSON.stringify(h.text)}`)
    expect(
      bad,
      'Shipped browser code is searching for a phrase. If the text came off the wire, this decides what ' +
        'to render by reading a sentence somebody else can reword — ask a typed field instead. If the ' +
        "phrase is genuinely the browser's own, or part of a format rather than a message, add it to " +
        'PHRASE_SEARCH_EXCEPTIONS with the reason.\n  ' + bad.join('\n  '),
    ).toEqual([])
  })

  it('every written-down phrase is still searched for — a stale entry switches the guard off quietly', () => {
    const present = new Set(HITS.filter((h) => h.kind === 'phrase-search').map((h) => `${h.file} ${h.text}`))
    const stale: string[] = []
    for (const e of PHRASE_SEARCH_EXCEPTIONS) {
      if (SOURCES[e.file] === undefined) {
        stale.push(`${e.file} does not exist any more`)
        continue
      }
      for (const p of e.phrases) {
        if (!present.has(`${e.file} ${p}`)) stale.push(`${e.file}  ${JSON.stringify(p)} is not searched for`)
      }
    }
    expect(
      stale,
      'An entry permits something that is not happening. Drop it — a list that keeps entries nobody can ' +
        'find stops being a list of anything.\n  ' + stale.join('\n  '),
    ).toEqual([])
  })

  it('every exception says WHY, at length enough to be a reason', () => {
    for (const e of PHRASE_SEARCH_EXCEPTIONS) {
      expect(e.why.length, `${e.file} has no reason written down`).toBeGreaterThan(80)
    }
  })

  it('catches each shape — comparison, method match, and map key', () => {
    expect(displayTextRoutingHits({ '/src/p.ts': 'if (n.title === X) {}' })).toHaveLength(1)
    expect(displayTextRoutingHits({ '/src/p.ts': "if (n.title.includes('x')) {}" })).toHaveLength(1)
    expect(displayTextRoutingHits({ '/src/p.ts': 'map[n.title] = n.description' })).toHaveLength(1)
  })

  it('catches the three fields the ruling names, not just the first', () => {
    expect(displayTextRoutingHits({ '/src/p.ts': "if (e.reason.includes('x')) {}" })).toHaveLength(1)
    expect(displayTextRoutingHits({ '/src/p.ts': "if (e.message.includes('x')) {}" })).toHaveLength(1)
    expect(displayTextRoutingHits({ '/src/p.ts': 'if (v.detail === X) {}' })).toHaveLength(1)
    expect(displayTextRoutingHits({ '/src/p.ts': 'if (v.headline !== X) {}' })).toHaveLength(1)
  })

  it('catches each of the three defects this round found, in the shape it actually shipped in', () => {
    // The wizard: a substring of the server's diagnostic decided the panel.
    expect(displayTextRoutingHits({ '/src/p.ts': "probeDetail.includes('sync= ')" })).toHaveLength(1)
    // The cluster page: the server's error sentence decided the empty state.
    expect(
      displayTextRoutingHits({ '/src/p.ts': "const lower = error.toLowerCase(); if (lower.includes('not found')) {}" }),
    ).toHaveLength(1)
    // The adopt dialog: five phrases hunted in the server's error_message.
    expect(displayTextRoutingHits({ '/src/p.ts': "lower.includes('no credentials available')" })).toHaveLength(1)
  })

  it('does not flag a comparison against a wire value, or a typeof check', () => {
    // Codes have no spaces, and `reason`/`message` are not compared by this guard.
    expect(displayTextRoutingHits({ '/src/p.ts': "if (repo.reason === 'no_connection') {}" })).toEqual([])
    expect(displayTextRoutingHits({ '/src/p.ts': "if (typeof d.message === 'string') {}" })).toEqual([])
    expect(displayTextRoutingHits({ '/src/p.ts': 'if (list.includes(row.state)) {}' })).toEqual([])
  })

  it('does not flag an array literal holding a title, or plain rendering', () => {
    expect(displayTextRoutingHits({ '/src/p.ts': 'const parts = [item.title]' })).toEqual([])
    expect(displayTextRoutingHits({ '/src/p.ts': 'return <p>{n.title}</p>' })).toEqual([])
  })

  it('ignores a routing shape written inside a comment', () => {
    expect(displayTextRoutingHits({ '/src/p.ts': '// if (n.title === X) {}\nconst a = 1' })).toEqual([])
    expect(displayTextRoutingHits({ '/src/p.ts': "// lower.includes('not found')\nconst a = 1" })).toEqual([])
  })

  it('reads shipped code only — a test may quote and compare whatever it needs to', () => {
    expect(displayTextRoutingHits({ '/src/views/__tests__/p.test.ts': 'if (n.title === X) {}' })).toEqual([])
    expect(displayTextRoutingHits({ '/src/p.test.ts': "lower.includes('not found')" })).toEqual([])
  })
})
