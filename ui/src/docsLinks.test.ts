// Every link in the UI (and in user-facing Go strings) that points at the
// Sharko docs site or at the Sharko repo on GitHub must point at something
// that really exists in this repo. A dead help link is only found by a user
// clicking it, so this test finds it first.
//
// Rules:
//  - https://sharko.readthedocs.io/en/latest/<path>/  ->  docs/site/<path>.md
//    or docs/site/<path>/index.md (mkdocs docs_dir is docs/site). A #fragment
//    must match a heading in that page.
//  - https://github.com/<owner>/sharko/(blob|tree)/main/<path>  ->  <path>
//    must exist in the repo, and <owner> must be MoranWeissman.
/// <reference types="node" />
import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'

const OWNER = 'MoranWeissman'

// This file lives in ui/src, so the repo root is two folders up.
const REPO_ROOT = resolve(__dirname, '..', '..')
const SKIP_DIRS = new Set(['node_modules', '.git', '.worktrees', '_dist'])

// Every file in the repo, as paths relative to the repo root.
function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(e.name)) continue
    const full = join(dir, e.name)
    if (e.isDirectory()) walk(full, out)
    else out.push(relative(REPO_ROOT, full).split('\\').join('/'))
  }
  return out
}

const repoFiles = walk(REPO_ROOT)
const repoFileSet = new Set(repoFiles)
const repoDirSet = new Set<string>()
for (const f of repoFiles) {
  const parts = f.split('/')
  for (let i = 1; i < parts.length; i++) repoDirSet.add(parts.slice(0, i).join('/'))
}

// Files that can hold a user-facing link: UI source and Go strings, never tests.
const sources: Record<string, string> = {}
for (const f of repoFiles) {
  const isUi = f.startsWith('ui/src/') && /\.(ts|tsx)$/.test(f) && !/\.test\.(ts|tsx)$/.test(f) && !f.includes('/__tests__/')
  const isGo = (f.startsWith('internal/') || f.startsWith('cmd/')) && f.endsWith('.go') && !f.endsWith('_test.go')
  if (isUi || isGo) sources[f] = readFileSync(join(REPO_ROOT, f), 'utf8')
}

const docPages: Record<string, string> = {}
for (const f of repoFiles) {
  if (f.startsWith('docs/site/') && f.endsWith('.md')) docPages[f] = readFileSync(join(REPO_ROOT, f), 'utf8')
}

// Make heading ids the way mkdocs does (Python-Markdown "toc" extension, the
// default slugify; mkdocs.yml sets no custom one):
//  - a heading may carry its own id: "## Title {#my-id}"; other "{...}"
//    attribute lists are dropped from the text (attr_list extension)
//  - otherwise: unicode NFKD, drop non-ascii, drop anything that is not a
//    word character, space or hyphen, trim, lowercase, and turn every run of
//    spaces and hyphens into one hyphen
//  - a second heading with the same id gets _1, a third _2, and so on
//  - lines inside fenced code blocks (``` or ~~~) are not headings
function slug(text: string): string {
  const plain = text
    .normalize('NFKD')
    .replace(/[^\x00-\x7f]/g, '')
    .replace(/[^\w\s-]/g, '')
    .trim()
    .toLowerCase()
  return plain.replace(/[-\s]+/g, '-')
}

// Inline markdown the reader never sees in the id: links, emphasis, html tags.
// Text inside `code` is kept exactly as written (so "<cluster>" in backticks
// stays, while a bare <tag> is html and disappears).
function plainText(md: string): string {
  return md
    .split(/(`+[^`]*`+)/)
    .map((part, i) =>
      i % 2 === 1
        ? part.replace(/^`+|`+$/g, '')
        : part
            .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
            .replace(/\[([^\]]*)\]\[[^\]]*\]/g, '$1')
            .replace(/<[^>]+>/g, '')
            .replace(/(\*\*|\*|__)/g, ''),
    )
    .join('')
}

// Same rule as Python-Markdown's toc "unique": add _1, or bump an existing _N.
function unique(id: string, used: Set<string>): string {
  while (used.has(id) || !id) {
    const m = /^(.*)_([0-9]+)$/.exec(id)
    id = m ? `${m[1]}_${Number(m[2]) + 1}` : `${id}_1`
  }
  used.add(id)
  return id
}

function anchorsOf(md: string): Set<string> {
  const used = new Set<string>()
  // A fence closes only on a line of the same character, at least as long,
  // with nothing after it (so "```promql" inside a fence is just text).
  let fence: string | null = null
  for (const line of md.replace(/<!--[\s\S]*?-->/g, (c) => c.replace(/[^\n]/g, '')).split('\n')) {
    const f = /^\s*(`{3,}|~{3,})(.*)$/.exec(line)
    if (fence === null && f) {
      fence = f[1]
      continue
    }
    if (fence !== null) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length && f[2].trim() === '') fence = null
      continue
    }
    const m = /^\s*#{1,6}\s+(.*?)\s*#*\s*$/.exec(line)
    if (!m) continue
    let text = m[1]
    let explicit: string | undefined
    const attr = /\s*\{([^{}]*)\}\s*$/.exec(text)
    if (attr) {
      explicit = /(?:^|\s)#([^\s{}]+)/.exec(attr[1])?.[1]
      text = text.slice(0, attr.index)
    }
    unique(explicit ?? slug(plainText(text)), used)
  }
  return used
}

// A link may be built as `...latest${SOME_CONST}` where the const is a plain
// string in the same file. Fill those in so the whole link is checked.
function resolveTemplates(url: string, text: string): string {
  return url.replace(/\$\{(\w+)\}/g, (whole, name: string) => {
    const m = new RegExp(`const\\s+${name}\\s*=\\s*['"\`]([^'"\`$]*)['"\`]`).exec(text)
    return m ? m[1] : whole
  })
}

interface Found {
  file: string
  url: string
}

// Find links in any form a person could write: http or https, with or without
// www., any capital letters in the host and in the word "sharko".
const DOCS_HOST = 'sharko.readthedocs.io'
const DOCS_PREFIX = `https://${DOCS_HOST}/en/latest/`
const DOCS_RE = /https?:\/\/(?:www\.)?sharko\.readthedocs\.io[^\s'"`)<>]*/gi
const GITHUB_RE = /https?:\/\/(?:www\.)?github\.com\/([\w.-]+)\/sharko(?![\w.-])[^\s'"`)<>]*/gi

// Turn a found link into one standard form (https, no www., lower-case host)
// so the checks below only have to know one shape. The path after the host
// keeps its capital letters: file names on GitHub and the docs site are
// case-sensitive, so a wrong-case path really is a dead link.
function normaliseDocs(url: string): string {
  return url.replace(/^https?:\/\/(?:www\.)?sharko\.readthedocs\.io/i, `https://${DOCS_HOST}`)
}
function normaliseGithub(url: string): string {
  return url.replace(/^https?:\/\/(?:www\.)?github\.com\/([\w.-]+)\/sharko(?![\w.-])/i, 'https://github.com/$1/sharko')
}

const docs: Found[] = []
const repo: Found[] = []
const owners: Found[] = []
for (const [file, text] of Object.entries(sources)) {
  for (const m of text.matchAll(DOCS_RE)) {
    docs.push({ file, url: normaliseDocs(resolveTemplates(m[0], text).replace(/[.,;:]+$/, '')) })
  }
  for (const m of text.matchAll(GITHUB_RE)) {
    const url = normaliseGithub(m[0].replace(/[.,;:]+$/, ''))
    owners.push({ file, url })
    if (/^https:\/\/github\.com\/[\w.-]+\/sharko\/(?:blob|tree)\//.test(url)) repo.push({ file, url })
  }
}

describe('links to the Sharko docs and repo', () => {
  it('finds links to check (so this test cannot pass by scanning nothing)', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(50)
    expect(docs.length).toBeGreaterThanOrEqual(5)
    expect(repo.length).toBeGreaterThanOrEqual(2)
    expect(Object.keys(docPages).length).toBeGreaterThan(20)
    expect(repoFileSet.has('catalog/addons.yaml')).toBe(true)
  })

  it('every docs site link points at a page that exists in docs/site', () => {
    const bad: string[] = []
    for (const { file, url } of docs) {
      const m = /^https:\/\/sharko\.readthedocs\.io\/en\/latest\/([^#?]*)(?:\?[^#]*)?(?:#(.*))?$/.exec(url)
      if (!m) {
        bad.push(`${url} (in ${file}) does not start with ${DOCS_PREFIX}`)
        continue
      }
      const path = m[1].replace(/\/$/, '')
      const anchor = m[2]
      const candidates = path === '' ? ['index.md'] : [`${path}.md`, `${path}/index.md`]
      const hit = candidates.find((c) => `docs/site/${c}` in docPages)
      if (!hit) {
        bad.push(`${url} (in ${file}) has no page: expected docs/site/${candidates.join(' or docs/site/')}`)
        continue
      }
      if (anchor && !anchorsOf(docPages[`docs/site/${hit}`]).has(anchor)) {
        bad.push(`${url} (in ${file}) page docs/site/${hit} has no heading for #${anchor}`)
      }
    }
    expect(bad, `Dead docs links:\n${bad.join('\n')}`).toEqual([])
  })

  it('every Sharko repo link points at a file or folder that exists', () => {
    const bad: string[] = []
    for (const { file, url } of repo) {
      // Only links to the main branch are accepted. A link to any other branch
      // (master, a tag, a commit) cannot be proven from this checkout, and a
      // branch that is deleted later turns the link dead. The stricter rule is
      // the honest one, so such a link fails and must be changed to main.
      const m = /^https:\/\/github\.com\/[\w.-]+\/sharko\/(?:blob|tree)\/main\/([^#?]*)/.exec(url)
      if (!m) {
        bad.push(`${url} (in ${file}) does not point at the main branch (use /blob/main/ or /tree/main/)`)
        continue
      }
      const path = m[1].replace(/\/$/, '')
      if (!path || !(repoFileSet.has(path) || repoDirSet.has(path))) {
        bad.push(`${url} (in ${file}) has no file or folder at ${path || '(empty path)'}`)
      }
    }
    expect(bad, `Dead repo links:\n${bad.join('\n')}`).toEqual([])
  })

  it(`every link to the Sharko repo on GitHub uses the owner ${OWNER}`, () => {
    const bad = owners
      .filter(({ url }) => !url.startsWith(`https://github.com/${OWNER}/sharko`))
      .map(({ file, url }) => `${url} (in ${file}) has the wrong owner, expected ${OWNER}`)
    expect(bad, `Wrong-owner links:\n${bad.join('\n')}`).toEqual([])
  })
})
