/**
 * RichMarkdown — sanitized rich-markdown renderer for third-party README content.
 *
 * Pipeline:
 *   react-markdown (GFM via remark-gfm)
 *   → rehype-raw   (allow embedded HTML from source)
 *   → rehype-sanitize (strip every executable/dangerous vector)
 *
 * Security model: input is treated as HOSTILE. The sanitize schema is
 * default-deny (GitHub schema as base), extended ONLY to preserve the visual
 * elements real CNCF READMEs need (badge images, logo paragraphs, alignment).
 * Anything not in the allowlist is stripped silently. XSS vectors that MUST
 * NOT survive: script/iframe/object/embed/form, on* handlers, javascript:/
 * data: URLs, style attributes.
 *
 * Use ONLY for third-party README content (ArtifactHub / upstream projects).
 * For internal/AI-generated markdown use MarkdownRenderer instead.
 */
import type { ComponentPropsWithoutRef } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize'
import type { Schema } from 'hast-util-sanitize'

/**
 * Sanitization schema — GitHub defaultSchema + minimal visual extensions.
 *
 * ADDITIONS over defaultSchema:
 *   attributes:
 *     - img: alt, width, height (already has src via default)
 *     - a: href restricted to http/https/mailto via protocols (already in default)
 *     - p, div, span, td, th: align (legacy badge/logo centering)
 *   tagNames:
 *     - span (already in default)
 *     - picture, source (already in default)
 *     - details, summary (already in default)
 *     - sup, sub (already in default)
 *     - br, hr (already in default)
 *
 * STRIPS (guaranteed absent from output):
 *   - style attribute on ALL elements (removed from wildcard allowlist)
 *   - script, iframe, object, embed, form, input (via strip/tagNames exclusion)
 *   - on* handlers (not in any attribute list)
 *   - javascript: and data: URLs (protocols allowlist is http/https/mailto only)
 *   - HTML comments (dropped by rehype-raw before sanitize runs)
 */
const sanitizeSchema: Schema = {
  ...defaultSchema,
  // Strip dangerous tags by not including them in tagNames.
  // defaultSchema.strip already has 'script'; we extend it.
  strip: [...(defaultSchema.strip ?? []), 'iframe', 'object', 'embed', 'form'],
  tagNames: [
    // Keep everything the GitHub schema allows, minus input (checkboxes are
    // fine but we drop the tag entirely to block any form-adjacent usage).
    ...(defaultSchema.tagNames ?? []).filter((t) => t !== 'input'),
  ],
  attributes: {
    ...defaultSchema.attributes,
    // img: allow alt, width, height in addition to what the schema already has.
    // src protocol is enforced by the protocols block below (http/https only).
    img: [
      ...(defaultSchema.attributes?.img ?? []),
      'alt',
      'width',
      'height',
    ],
    // a: href is already in the default; protocol enforcement below limits to
    // http/https/mailto.
    a: [...(defaultSchema.attributes?.a ?? [])],
    // Allow legacy align attribute on block/table elements for badge/logo rows.
    p: [...(defaultSchema.attributes?.p ?? []), 'align'],
    div: [...(defaultSchema.attributes?.div ?? []), 'align'],
    td: [...(defaultSchema.attributes?.td ?? []), 'align'],
    th: [...(defaultSchema.attributes?.th ?? []), 'align'],
    // source: allow srcset for <picture> elements (http/https only via protocols).
    source: ['srcSet'],
    '*': (defaultSchema.attributes?.['*'] ?? []).filter(
      // Strip the style attribute from the global wildcard allowlist.
      // KEDA uses style="font-size: 25px" — we drop the attribute, the text
      // content still renders.  A broken badge is acceptable; CSS injection is not.
      (attr) => attr !== 'style' && !(Array.isArray(attr) && attr[0] === 'style'),
    ),
  },
  protocols: {
    ...defaultSchema.protocols,
    // Restrict img src to http/https only — blocks data: and javascript: URIs.
    src: ['http', 'https'],
    // Restrict srcset to http/https only.
    srcSet: ['http', 'https'],
    // Restrict href to http/https/mailto — blocks javascript: URIs.
    href: ['http', 'https', 'mailto'],
  },
}

// ─── Component mappings ────────────────────────────────────────────────────
// We enforce security-relevant attributes via React component props so that
// the sanitize schema is our first wall, and the component mapping is our
// second — neither alone is sufficient.

function RichLink({
  href,
  children,
  ...rest
}: ComponentPropsWithoutRef<'a'>) {
  // Only render links with safe protocols — anything that survived sanitize
  // but still looks wrong gets dropped to a plain span.
  const safe =
    !href ||
    href.startsWith('http://') ||
    href.startsWith('https://') ||
    href.startsWith('mailto:')
  if (!safe) {
    return <span>{children}</span>
  }
  return (
    <a
      {...rest}
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="text-teal-600 underline hover:text-teal-700 dark:text-teal-400"
    >
      {children}
    </a>
  )
}

function RichImage({ src, alt, ...rest }: ComponentPropsWithoutRef<'img'>) {
  // Only render images from http/https — belt-and-suspenders after sanitize.
  const safe =
    !src || src.startsWith('http://') || src.startsWith('https://')
  if (!safe) {
    // Render alt text so the page degrades gracefully.
    return <span className="italic text-[#3a6a8a]">{alt ?? ''}</span>
  }
  return (
    <img
      {...rest}
      src={src}
      alt={alt ?? ''}
      loading="lazy"
      className="inline max-w-full align-middle"
      onError={(e) => {
        // Broken third-party images: show alt text, no layout blowup.
        const img = e.currentTarget
        img.style.display = 'none'
        const span = document.createElement('span')
        span.className = 'text-xs italic text-[#3a6a8a]'
        span.textContent = alt ?? ''
        img.parentNode?.insertBefore(span, img.nextSibling)
      }}
    />
  )
}

// ─── Public component ──────────────────────────────────────────────────────

interface RichMarkdownProps {
  /** Raw markdown string that may contain embedded HTML (third-party READMEs). */
  content: string
}

export function RichMarkdown({ content }: RichMarkdownProps) {
  return (
    <div className="rich-markdown space-y-2 text-sm leading-relaxed">
      <Markdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeRaw, [rehypeSanitize, sanitizeSchema]]}
        components={{
          a: RichLink,
          img: RichImage,
          // Headings — match app palette.
          h1: ({ children, ...rest }) => (
            <h1
              {...rest}
              className="mt-4 text-lg font-bold text-[#0a2a4a] dark:text-gray-100"
            >
              {children}
            </h1>
          ),
          h2: ({ children, ...rest }) => (
            <h2
              {...rest}
              className="mt-5 mb-2 border-b border-[#6aade0] pb-1.5 text-base font-bold text-[#0a2a4a] dark:border-gray-700 dark:text-gray-100"
            >
              {children}
            </h2>
          ),
          h3: ({ children, ...rest }) => (
            <h3
              {...rest}
              className="mt-4 mb-1.5 text-sm font-bold text-[#0a2a4a] dark:text-gray-100"
            >
              {children}
            </h3>
          ),
          h4: ({ children, ...rest }) => (
            <h4
              {...rest}
              className="mt-3 mb-1 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100"
            >
              {children}
            </h4>
          ),
          // Body text.
          p: ({ children, ...rest }) => (
            <p {...rest} className="text-[#2a5a7a] dark:text-gray-300">
              {children}
            </p>
          ),
          // Inline code chips.
          code: ({ children, className, ...rest }) => {
            // react-markdown sets a language-* className on fenced blocks.
            const isBlock = className?.startsWith('language-')
            if (isBlock) {
              return (
                <code className={className} {...rest}>
                  {children}
                </code>
              )
            }
            return (
              <code
                {...rest}
                className="rounded bg-[#c0ddf0] px-1 py-0.5 text-xs dark:bg-gray-700"
              >
                {children}
              </code>
            )
          },
          // Code blocks.
          pre: ({ children, ...rest }) => (
            <pre
              {...rest}
              className="my-2 overflow-x-auto rounded-lg bg-[#071828] p-3 text-xs leading-relaxed text-[#5a8aaa]"
            >
              {children}
            </pre>
          ),
          // Blockquote.
          blockquote: ({ children, ...rest }) => (
            <blockquote
              {...rest}
              className="border-l-4 border-[#6aade0] pl-4 italic text-[#3a6a8a] dark:border-gray-600 dark:text-gray-400"
            >
              {children}
            </blockquote>
          ),
          // Tables — GFM.
          table: ({ children, ...rest }) => (
            <div className="overflow-x-auto">
              <table
                {...rest}
                className="min-w-full border-collapse text-sm text-[#2a5a7a] dark:text-gray-300"
              >
                {children}
              </table>
            </div>
          ),
          th: ({ children, ...rest }) => (
            <th
              {...rest}
              className="border border-[#b0cce0] bg-[#e0eef9] px-3 py-1.5 text-left font-semibold text-[#0a2a4a] dark:border-gray-700 dark:bg-gray-800 dark:text-gray-100"
            >
              {children}
            </th>
          ),
          td: ({ children, ...rest }) => (
            <td
              {...rest}
              className="border border-[#c0ddf0] px-3 py-1.5 dark:border-gray-700"
            >
              {children}
            </td>
          ),
          // Horizontal rule.
          hr: (rest) => (
            <hr {...rest} className="my-4 border-[#c0ddf0] dark:border-gray-700" />
          ),
          // Lists.
          ul: ({ children, ...rest }) => (
            <ul {...rest} className="ml-5 list-disc space-y-1 text-[#2a5a7a] dark:text-gray-300">
              {children}
            </ul>
          ),
          ol: ({ children, ...rest }) => (
            <ol {...rest} className="ml-5 list-decimal space-y-1 text-[#2a5a7a] dark:text-gray-300">
              {children}
            </ol>
          ),
        }}
      >
        {content}
      </Markdown>
    </div>
  )
}
