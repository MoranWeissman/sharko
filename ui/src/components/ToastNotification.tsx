import { useState, useEffect, useCallback } from 'react'
import { X, CheckCircle2 } from 'lucide-react'
import { PRLink } from '@/components/PRFeedback'

/**
 * Optional clickable PR link attached to a toast. When present the toast
 * renders a real "View PR #N on GitHub" anchor under the message instead of
 * baking a dead, non-clickable URL into the message string (V2-cleanup-24).
 */
export interface ToastPRLink {
  url: string
  id?: number | null
}

interface Toast {
  id: number
  message: string
  type: 'success' | 'info'
  pr?: ToastPRLink
}

let toastId = 0
let addToastFn:
  | ((message: string, type?: 'success' | 'info', pr?: ToastPRLink) => void)
  | null = null

/**
 * showToast — fire a transient toast. Pass an optional `pr` ({ url, id }) to
 * render a clickable "View PR #N on GitHub" link beneath the message; this is
 * how every write flow surfaces its PR consistently (no more dead "PR #N →"
 * text in the toast string).
 */
export function showToast(
  message: string,
  type: 'success' | 'info' = 'success',
  pr?: ToastPRLink,
) {
  addToastFn?.(message, type, pr)
}

export function ToastContainer() {
  const [toasts, setToasts] = useState<Toast[]>([])

  const addToast = useCallback(
    (message: string, type: 'success' | 'info' = 'success', pr?: ToastPRLink) => {
      const id = ++toastId
      setToasts(prev => [...prev, { id, message, type, pr }])
      setTimeout(() => {
        setToasts(prev => prev.filter(t => t.id !== id))
      }, 6000)
    },
    [],
  )

  useEffect(() => {
    addToastFn = addToast
    return () => { addToastFn = null }
  }, [addToast])

  const removeToast = (id: number) => {
    setToasts(prev => prev.filter(t => t.id !== id))
  }

  if (toasts.length === 0) return null

  return (
    <div className="fixed bottom-4 right-4 z-[100] flex flex-col gap-2">
      {toasts.map(toast => (
        <div
          key={toast.id}
          className="flex items-start gap-3 rounded-lg bg-[#f0f7ff] px-4 py-3 shadow-lg ring-2 ring-[#6aade0] dark:bg-gray-800 dark:ring-gray-700 animate-in slide-in-from-right"
          style={{ minWidth: 280, maxWidth: 420 }}
        >
          <CheckCircle2 className={`mt-0.5 h-4 w-4 shrink-0 ${
            toast.type === 'success' ? 'text-green-500' : 'text-teal-500'
          }`} />
          <div className="flex-1">
            <p className="text-sm text-[#0a2a4a] dark:text-gray-100">{toast.message}</p>
            {toast.pr && (
              <PRLink
                url={toast.pr.url}
                id={toast.pr.id}
                className="mt-1 text-teal-600 dark:text-teal-400"
              />
            )}
          </div>
          <button
            onClick={() => removeToast(toast.id)}
            className="shrink-0 rounded p-0.5 text-[#3a6a8a] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:text-white"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
    </div>
  )
}
