import { useState, useEffect, useRef, useCallback } from 'react'
import { Bell } from 'lucide-react'
import { api } from '@/services/api'

interface Notification {
  id: string
  type: 'upgrade' | 'security' | 'drift'
  title: string
  description: string
  timestamp: string
  read: boolean
}

export function NotificationBell() {
  const [open, setOpen] = useState(false)
  const [notifications, setNotifications] = useState<Notification[]>([])
  const ref = useRef<HTMLDivElement>(null)

  // Fetch notifications from API
  const fetchNotifications = useCallback(() => {
    api.getNotifications()
      .then(data => {
        setNotifications((data.notifications ?? []) as Notification[])
      })
      .catch(() => {
        // API not available — keep whatever we have
      })
  }, [])

  useEffect(() => {
    fetchNotifications()
    const interval = setInterval(fetchNotifications, 60000) // poll every 60s
    return () => clearInterval(interval)
  }, [fetchNotifications])

  // Close dropdown when clicking outside
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const unreadCount = notifications.filter(n => !n.read).length

  const markAllRead = () => {
    api.markAllNotificationsRead()
      .then(() => {
        setNotifications(prev => prev.map(n => ({ ...n, read: true })))
      })
      .catch(() => {
        // Optimistic — mark locally even if API fails
        setNotifications(prev => prev.map(n => ({ ...n, read: true })))
      })
  }

  const timeAgo = (ts: string) => {
    const secs = Math.floor((Date.now() - new Date(ts).getTime()) / 1000)
    if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
    if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
    return `${Math.floor(secs / 86400)}d ago`
  }

  const typeIcon = (type: string) => {
    switch (type) {
      case 'security': return '🔒'
      case 'upgrade': return '⬆️'
      case 'drift': return '⚠️'
      default: return 'ℹ️'
    }
  }

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(o => !o)}
        className="relative flex items-center justify-center rounded-lg p-2 text-[#2a5a7a] hover:bg-[#d6eeff] transition-colors"
        aria-label="Notifications"
      >
        <Bell className="h-5 w-5" />
        {unreadCount > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 w-4 items-center justify-center rounded-full bg-red-500 text-[9px] font-bold text-white">
            {unreadCount}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 top-12 z-50 w-80 rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-xl dark:bg-gray-800 dark:ring-gray-700">
          <div className="flex items-center justify-between border-b border-[#6aade0] px-4 py-3">
            <h3 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              Notifications {unreadCount > 0 && `(${unreadCount})`}
            </h3>
            {unreadCount > 0 && (
              <button
                onClick={markAllRead}
                className="text-xs text-[#2a5a7a] hover:text-[#0a2a4a] dark:text-gray-400"
              >
                Mark all as read
              </button>
            )}
          </div>

          <div className="max-h-80 overflow-y-auto">
            {notifications.length === 0 ? (
              <p className="px-4 py-6 text-center text-sm text-[#3a6a8a]">
                No notifications
              </p>
            ) : (
              notifications.map(n => (
                <div
                  key={n.id}
                  className={`border-b border-[#d6eeff] px-4 py-3 last:border-0 ${
                    !n.read ? 'bg-[#e0f0ff]' : ''
                  }`}
                >
                  <div className="flex items-start gap-2">
                    <span className="mt-0.5 text-sm">{typeIcon(n.type)}</span>
                    <div className="min-w-0 flex-1">
                      <p className={`text-sm ${!n.read ? 'font-semibold text-[#0a2a4a]' : 'text-[#1a4a6a]'}`}>
                        {n.title}
                      </p>
                      <p className="mt-0.5 text-xs text-[#3a6a8a]">{n.description}</p>
                      <p className="mt-1 text-[10px] text-[#5a8aaa]">{timeAgo(n.timestamp)}</p>
                    </div>
                    {!n.read && (
                      <div className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-blue-500" />
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}
