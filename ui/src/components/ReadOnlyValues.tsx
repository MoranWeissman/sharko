import { ExternalLink } from 'lucide-react'

/**
 * ReadOnlyValues — the honest values screen for a repo in the new layout.
 *
 * Sharko does not edit values there in this version. The screen shows the
 * real file exactly as it is in Git (comments included, nothing hidden) and
 * says, in one plain sentence, where to change it. No Submit button.
 *
 * The sentences are exported so tests pin them word for word.
 */
export const READ_ONLY_VALUES_SENTENCE = (path: string) =>
  `Editing values here is not in this version. To change them, edit the file in your Git repo: ${path}`

export const VALUES_FILE_MISSING_SENTENCE = (path: string) =>
  `There is no file at ${path} in your Git repo yet. Create it there to set values.`

/** Title of the all-add-ons view on the cluster page of a v4 repo. */
export const CLUSTER_VALUES_V4_TITLE =
  'Cluster Values, grouped by add-on (the add-on names are headings added here, they are not written in the files)'

export const VALUES_FILE_EMPTY_SENTENCE = 'This file is empty.'

/** Shown in place of the file when reading it from Git failed. */
export const VALUES_READ_FAILED_SENTENCE =
  'Sharko could not read the values file from Git just now. Nothing was changed.'

export const VALUES_FILE_WRAPPED_SENTENCE = (addon: string) =>
  `None of the settings in this file take effect. They are all inside a block named "${addon}:". Move them out of that block so they start at the left edge of the file.`

export interface ReadOnlyValuesProps {
  title: string
  /** Path of the file in the Git repo. */
  path: string
  /** False when the file is not in the repo. */
  exists: boolean
  /** The file as it is in Git. */
  content: string
  /** Set when the file is wrapped under this add-on name. */
  wrappedUnderAddon?: string
  /** Link that opens the file on the Git host, when Sharko can build one. */
  fileURL?: string
  fileLinkLabel?: string
}

export function ReadOnlyValues({
  title,
  path,
  exists,
  content,
  wrappedUnderAddon,
  fileURL,
  fileLinkLabel = 'Open the file in your Git host',
}: ReadOnlyValuesProps) {
  const isEmpty = exists && content.trim() === ''
  return (
    <div
      data-testid="read-only-values"
      className="space-y-3 rounded-xl bg-[#f0f7ff] p-4 ring-2 ring-[#6aade0] dark:bg-gray-800 dark:ring-gray-700"
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">{title}</h3>
        {fileURL && (
          <a
            href={fileURL}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 rounded-md border border-[#6aade0] bg-[#e0f0ff] px-3 py-1 text-xs font-medium text-[#0a6aaa] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-[#6aade0] dark:hover:bg-gray-600"
          >
            <ExternalLink className="h-3 w-3" />
            {fileLinkLabel}
          </a>
        )}
      </div>

      <p className="text-sm text-[#2a5a7a] dark:text-gray-300">{READ_ONLY_VALUES_SENTENCE(path)}</p>

      {wrappedUnderAddon && (
        <p
          role="alert"
          className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-200"
        >
          {VALUES_FILE_WRAPPED_SENTENCE(wrappedUnderAddon)}
        </p>
      )}

      {!exists ? (
        <p className="text-sm italic text-[#3a6a8a] dark:text-gray-400">
          {VALUES_FILE_MISSING_SENTENCE(path)}
        </p>
      ) : isEmpty ? (
        <p className="text-sm italic text-[#3a6a8a] dark:text-gray-400">{VALUES_FILE_EMPTY_SENTENCE}</p>
      ) : (
        <pre
          data-testid="read-only-values-content"
          className="max-h-[32rem] overflow-auto whitespace-pre rounded-lg bg-[#071828] p-4 font-mono text-xs leading-relaxed text-[#7aaacc]"
        >
          {content}
        </pre>
      )}
    </div>
  )
}
