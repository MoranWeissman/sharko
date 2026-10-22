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

function slug(heading: string): string {
  return heading
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .replace(/\s+/g, '-')
}

function anchorsOf(md: string): Set<string> {
  const out = new Set<string>()
  for (const line of md.split('\n')) {
    const m = /^#{1,6}\s+(.*?)\s*#*\s*$/.exec(line)
    if (m) out.add(slug(m[1]))
  }
  return out
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

const docs: Found[] = []
const repo: Found[] = []
const owners: Found[] = []
for (const [file, text] of Object.entries(sources)) {
  for (const m of text.matchAll(/https:\/\/sharko\.readthedocs\.io[^\s'"`)<>]*/g)) {
    docs.push({ file, url: resolveTemplates(m[0], text).replace(/[.,;:]+$/, '') })
  }
  for (const m of text.matchAll(/https:\/\/github\.com\/([\w.-]+)\/sharko(?![\w.-])[^\s'"`)<>]*/g)) {
    const url = m[0].replace(/[.,;:]+$/, '')
    owners.push({ file, url })
    if (/\/(blob|tree)\/main\//.test(url)) repo.push({ file, url })
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
        bad.push(`${url} (in ${file}) does not start with https://sharko.readthedocs.io/en/latest/`)
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
      const m = /^https:\/\/github\.com\/[\w.-]+\/sharko\/(?:blob|tree)\/main\/([^#?]*)/.exec(url)
      const path = (m?.[1] ?? '').replace(/\/$/, '')
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
