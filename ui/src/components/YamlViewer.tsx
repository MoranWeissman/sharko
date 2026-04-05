import { useState, useCallback } from 'react'
import { parse } from 'yaml'
import { Copy, Check, ChevronRight, ChevronDown } from 'lucide-react'

interface YamlViewerProps {
  yaml: string
  title?: string
  defaultView?: ViewMode
}

type ViewMode = 'raw' | 'tree'

function highlightYaml(raw: string): string {
  return raw
    .split('\n')
    .map((line) => {
      // Comments
      if (/^\s*#/.test(line)) {
        return `<span class="text-[#2a5a7a]">${escapeHtml(line)}</span>`
      }
      // Key-value lines
      const match = line.match(/^(\s*)([\w.-]+)(\s*:\s*)(.*)$/)
      if (match) {
        const [, indent, key, colon, value] = match
        let valueHtml = escapeHtml(value)
        if (/^['"].*['"]$/.test(value) || /^[a-zA-Z]/.test(value) && !/^(true|false|null|~)$/i.test(value)) {
          valueHtml = `<span class="text-green-400">${escapeHtml(value)}</span>`
        } else if (/^-?\d+(\.\d+)?$/.test(value)) {
          valueHtml = `<span class="text-yellow-400">${escapeHtml(value)}</span>`
        } else if (/^(true|false)$/i.test(value)) {
          valueHtml = `<span class="text-blue-400">${escapeHtml(value)}</span>`
        } else if (/^(null|~)$/i.test(value)) {
          valueHtml = `<span class="text-[#2a5a7a]">${escapeHtml(value)}</span>`
        }
        return `${escapeHtml(indent)}<span class="text-teal-400">${escapeHtml(key)}</span>${escapeHtml(colon)}${valueHtml}`
      }
      // Array items
      const arrayMatch = line.match(/^(\s*-\s)(.*)$/)
      if (arrayMatch) {
        const [, prefix, value] = arrayMatch
        return `<span class="text-[#3a6a8a]">${escapeHtml(prefix)}</span><span class="text-green-400">${escapeHtml(value)}</span>`
      }
      return escapeHtml(line)
    })
    .join('\n')
}

function escapeHtml(str: string): string {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

function TreeNode({ name, value, defaultExpanded = true }: { name: string; value: unknown; defaultExpanded?: boolean }) {
  const [expanded, setExpanded] = useState(defaultExpanded)

  if (value === null || value === undefined) {
    return (
      <div className="flex items-center gap-1 py-0.5">
        <span className="w-4" />
        <span className="font-semibold text-teal-400">{name}:</span>
        <span className="text-[#2a5a7a]">null</span>
      </div>
    )
  }

  if (typeof value === 'object' && !Array.isArray(value)) {
    const entries = Object.entries(value as Record<string, unknown>)
    return (
      <div>
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="flex items-center gap-1 py-0.5 hover:bg-[#0d2035] rounded px-1 -ml-1"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-[#2a5a7a] shrink-0" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-[#2a5a7a] shrink-0" />
          )}
          <span className="font-semibold text-teal-400">{name}</span>
          {!expanded && (
            <span className="text-[#3a6a8a] text-xs ml-1">({entries.length} keys)</span>
          )}
        </button>
        {expanded && (
          <div className="ml-4 border-l border-[#1a3a5a] pl-2">
            {entries.map(([k, v]) => (
              <TreeNode key={k} name={k} value={v} defaultExpanded={false} />
            ))}
          </div>
        )}
      </div>
    )
  }

  if (Array.isArray(value)) {
    return (
      <div>
        <button
          type="button"
          onClick={() => setExpanded(!expanded)}
          className="flex items-center gap-1 py-0.5 hover:bg-[#0d2035] rounded px-1 -ml-1"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-[#2a5a7a] shrink-0" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-[#2a5a7a] shrink-0" />
          )}
          <span className="font-semibold text-teal-400">{name}</span>
          <span className="text-[#3a6a8a] text-xs ml-1">[{value.length}]</span>
        </button>
        {expanded && (
          <div className="ml-4 border-l border-[#1a3a5a] pl-2">
            {value.map((item, i) => (
              <TreeNode key={i} name={`[${i}]`} value={item} defaultExpanded={false} />
            ))}
          </div>
        )}
      </div>
    )
  }

  let valueClass = 'text-green-400'
  if (typeof value === 'number') valueClass = 'text-yellow-400'
  if (typeof value === 'boolean') valueClass = 'text-blue-400'

  return (
    <div className="flex items-center gap-1 py-0.5">
      <span className="w-4 shrink-0" />
      <span className="font-semibold text-teal-400">{name}:</span>
      <span className={valueClass}>{String(value)}</span>
    </div>
  )
}

export function YamlViewer({ yaml: yamlString, title, defaultView = 'raw' }: YamlViewerProps) {
  const [mode, setMode] = useState<ViewMode>(defaultView)
  const [copied, setCopied] = useState(false)

  const handleCopy = useCallback(async () => {
    await navigator.clipboard.writeText(yamlString)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [yamlString])

  let parsedObj: unknown = null
  if (mode === 'tree') {
    try {
      parsedObj = parse(yamlString)
    } catch {
      // Fall back to raw if parsing fails
      parsedObj = null
    }
  }

  return (
    <div className="rounded-xl border border-[#90c8ee] bg-[#f0f7ff] shadow-sm dark:border-[#1a3a5a] dark:bg-gray-800">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-[#90c8ee] px-4 py-3 dark:border-[#1a3a5a]">
        <div>
          {title && (
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              {title}
            </h3>
          )}
        </div>
        <div className="flex items-center gap-2">
          {/* Copy button */}
          <button
            type="button"
            onClick={handleCopy}
            className="inline-flex items-center gap-1 rounded-md border border-[#80b8e0] px-2 py-1 text-xs font-medium text-[#1a4a6a] transition-colors hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-400 dark:hover:bg-gray-700"
            aria-label="Copy YAML"
          >
            {copied ? (
              <>
                <Check className="h-3.5 w-3.5 text-green-500" />
                Copied
              </>
            ) : (
              <>
                <Copy className="h-3.5 w-3.5" />
                Copy
              </>
            )}
          </button>

          {/* View mode toggle */}
          <div className="flex rounded-md border border-[#80b8e0] dark:border-gray-600">
            <button
              type="button"
              onClick={() => setMode('raw')}
              className={`px-2 py-1 text-xs font-medium transition-colors ${
                mode === 'raw'
                  ? 'bg-teal-500 text-white'
                  : 'text-[#1a4a6a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700'
              } rounded-l-md`}
            >
              Raw
            </button>
            <button
              type="button"
              onClick={() => setMode('tree')}
              className={`px-2 py-1 text-xs font-medium transition-colors ${
                mode === 'tree'
                  ? 'bg-teal-500 text-white'
                  : 'text-[#1a4a6a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700'
              } rounded-r-md`}
            >
              Tree
            </button>
          </div>
        </div>
      </div>

      {/* Content */}
      <div className="overflow-x-auto rounded-b-xl bg-[#071828] p-4 text-[#7aaacc]">
        {mode === 'raw' ? (
          <pre
            className="font-mono text-xs leading-relaxed"
            dangerouslySetInnerHTML={{ __html: highlightYaml(yamlString) }}
          />
        ) : parsedObj !== null && typeof parsedObj === 'object' ? (
          <div className="font-mono text-xs leading-relaxed">
            {Object.entries(parsedObj as Record<string, unknown>).map(([k, v]) => (
              <TreeNode key={k} name={k} value={v} defaultExpanded={true} />
            ))}
          </div>
        ) : (
          <pre className="font-mono text-xs leading-relaxed text-[#3a6a8a]">
            Unable to parse YAML for tree view. Showing raw content:{'\n\n'}
            {yamlString}
          </pre>
        )}
      </div>
    </div>
  )
}
