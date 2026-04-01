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
        return `<span class="text-gray-500">${escapeHtml(line)}</span>`
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
          valueHtml = `<span class="text-gray-500">${escapeHtml(value)}</span>`
        }
        return `${escapeHtml(indent)}<span class="text-cyan-400">${escapeHtml(key)}</span>${escapeHtml(colon)}${valueHtml}`
      }
      // Array items
      const arrayMatch = line.match(/^(\s*-\s)(.*)$/)
      if (arrayMatch) {
        const [, prefix, value] = arrayMatch
        return `<span class="text-gray-400">${escapeHtml(prefix)}</span><span class="text-green-400">${escapeHtml(value)}</span>`
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
        <span className="font-semibold text-cyan-400">{name}:</span>
        <span className="text-gray-500">null</span>
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
          className="flex items-center gap-1 py-0.5 hover:bg-gray-800 rounded px-1 -ml-1"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-gray-500 shrink-0" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-gray-500 shrink-0" />
          )}
          <span className="font-semibold text-cyan-400">{name}</span>
          {!expanded && (
            <span className="text-gray-400 text-xs ml-1">({entries.length} keys)</span>
          )}
        </button>
        {expanded && (
          <div className="ml-4 border-l border-gray-700 pl-2">
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
          className="flex items-center gap-1 py-0.5 hover:bg-gray-800 rounded px-1 -ml-1"
        >
          {expanded ? (
            <ChevronDown className="h-3.5 w-3.5 text-gray-500 shrink-0" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5 text-gray-500 shrink-0" />
          )}
          <span className="font-semibold text-cyan-400">{name}</span>
          <span className="text-gray-400 text-xs ml-1">[{value.length}]</span>
        </button>
        {expanded && (
          <div className="ml-4 border-l border-gray-700 pl-2">
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
      <span className="font-semibold text-cyan-400">{name}:</span>
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
    <div className="rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-gray-700">
        <div>
          {title && (
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {title}
            </h3>
          )}
        </div>
        <div className="flex items-center gap-2">
          {/* Copy button */}
          <button
            type="button"
            onClick={handleCopy}
            className="inline-flex items-center gap-1 rounded-md border border-gray-300 px-2 py-1 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-gray-700"
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
          <div className="flex rounded-md border border-gray-300 dark:border-gray-600">
            <button
              type="button"
              onClick={() => setMode('raw')}
              className={`px-2 py-1 text-xs font-medium transition-colors ${
                mode === 'raw'
                  ? 'bg-cyan-500 text-white'
                  : 'text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
              } rounded-l-md`}
            >
              Raw
            </button>
            <button
              type="button"
              onClick={() => setMode('tree')}
              className={`px-2 py-1 text-xs font-medium transition-colors ${
                mode === 'tree'
                  ? 'bg-cyan-500 text-white'
                  : 'text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'
              } rounded-r-md`}
            >
              Tree
            </button>
          </div>
        </div>
      </div>

      {/* Content */}
      <div className="overflow-x-auto rounded-b-xl bg-gray-900 p-4 text-gray-100">
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
          <pre className="font-mono text-xs leading-relaxed text-gray-400">
            Unable to parse YAML for tree view. Showing raw content:{'\n\n'}
            {yamlString}
          </pre>
        )}
      </div>
    </div>
  )
}
