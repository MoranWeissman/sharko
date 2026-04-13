import { useState, useEffect, useCallback } from 'react'
import { X, CheckCircle2 } from 'lucide-react'

interface Toast {
  id: number
  message: string
  type: 'success' | 'info'
}

let toastId = 0
let addToastFn: ((message: string, type?: 'success' | 'info') => void) | null = null

export function showToast(message: string, type: 'success' | 'info' = 'success') {
  addToastFn?.(message, type)
}

export function ToastContainer() {
  const [toasts, setToasts] = useState<Toast[]>([])

  const addToast = useCallback((message: string, type: 'success' | 'info' = 'success') => {
    const id = ++toastId
    setToasts(prev => [...prev, { id, message, type }])
    setTimeout(() => {
      setToasts(prev => prev.filter(t => t.id !== id))
    }, 6000)
  }, [])

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
          <p className="flex-1 text-sm text-[#0a2a4a] dark:text-gray-100">{toast.message}</p>
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
