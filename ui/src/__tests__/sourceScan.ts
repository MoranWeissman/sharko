// sourceScan — the two primitives every browser-source guard needs, in one
// place so they cannot drift apart.
//
// Three tests under ui/src/__tests__ read the whole browser tree as text and
// look for a shape in it: serverSentenceCopies (a server sentence typed into
// browser code), browserAuthoredPromises (the browser promising what the
// server will do), and bannedWordingSweep (a retired phrase, which works on
// raw lines and needs neither of these). Each of them has to answer the same
// two questions first — where does a comment end, and where does a string
// literal begin — and a second hand-written copy of either answer is a guard
// that quietly stops agreeing with its sibling.
//
// So they live here, imported by both, and any future guard gets them for
// free. This module holds no policy of its own: no phrase list, no catalog,
// no verdict. Just the two answers.

/**
 * Removes `//` line comments and block comments, leaving string literals
 * intact. Comments quoting a server sentence go stale too, but they are
 * prose, not behaviour — they are named in the epic's backlog section, not
 * failed here, because banning a quotation in a comment would stop people
 * explaining what a change replaced.
 */
export function stripComments(source: string): string {
  const out: string[] = []
  let quote: string | null = null
  let i = 0
  while (i < source.length) {
    const c = source[i]
    if (quote === null) {
      if (c === '/' && source[i + 1] === '/') {
        while (i < source.length && source[i] !== '\n') i++
        continue
      }
      if (c === '/' && source[i + 1] === '*') {
        i += 2
        while (i + 1 < source.length && !(source[i] === '*' && source[i + 1] === '/')) i++
        i += 2
        continue
      }
      if (c === "'" || c === '"' || c === '`') quote = c
      out.push(c)
      i++
      continue
    }
    if (c === '\\') {
      out.push(source.slice(i, i + 2))
      i += 2
      continue
    }
    if (c === quote) quote = null
    out.push(c)
    i++
  }
  return out.join('')
}

/**
 * String literals of 7+ characters — "Blocked" is 7.
 *
 * ALL THREE QUOTE CHARACTERS, and the third one is the point. This pattern
 * knew only `'` and `"`, so a server sentence typed inside backticks was
 * invisible to the whole guard — `stripComments` kept the contents (it treats a
 * backtick as a quote, correctly), and then nothing ever looked at them. Two
 * separate reviewers demonstrated it the same way, on different files and
 * different sentences: paste a catalog sentence in single quotes and this test
 * goes red; change the two quotes to backticks and the identical paste passes.
 * A template literal with no substitution in it is an ordinary string, and
 * writing one is an everyday JavaScript habit, not an evasion — which is
 * exactly why the hole mattered.
 *
 * The backtick arm deliberately allows newlines where the other two do not: a
 * template literal legitimately spans lines. A template carrying a `${…}`
 * substitution is captured with the substitution still in it, so it can only
 * match a catalog sentence if the sentence really is spelled out around it.
 */
export const LITERAL = /(?<![\w$])(?:'((?:[^'\\\n]|\\.){7,})'|"((?:[^"\\\n]|\\.){7,})"|`((?:[^`\\]|\\.){7,})`)/g

// NOTE ON SHARING A /g REGEX. `LITERAL` carries the global flag, so it holds a
// `lastIndex`. Every consumer must use `String.matchAll`, which clones the
// regex internally and leaves `lastIndex` alone — never `.exec` in a loop and
// never `.test`, either of which would advance the shared object and make one
// guard's result depend on whether the other guard ran first.
