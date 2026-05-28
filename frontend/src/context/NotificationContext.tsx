import { createContext, useCallback, useContext, useRef, useState } from 'react'

export type NotifType = 'info' | 'success' | 'warn' | 'error' | 'progress'

export interface NotificationAction {
  label: string
  href: string
}

export interface Notification {
  id: string
  type: NotifType
  message: string
  jobId?: string // only for type === 'progress'
  action?: NotificationAction
}

interface NotificationContextValue {
  notify: (type: NotifType, message: string, action?: NotificationAction) => void
  notifyProgress: (jobId: string, message: string) => void
  dismiss: (id: string) => void
  notifications: Notification[]
}

const NotificationContext = createContext<NotificationContextValue | null>(null)

export function NotificationProvider({ children }: { children: React.ReactNode }) {
  const [notifications, setNotifications] = useState<Notification[]>([])
  const timers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())

  const dismiss = useCallback((id: string) => {
    setNotifications((prev) => prev.filter((n) => n.id !== id))
    const t = timers.current.get(id)
    if (t) { clearTimeout(t); timers.current.delete(id) }
  }, [])

  const notify = useCallback((type: NotifType, message: string, action?: NotificationAction) => {
    const id = Math.random().toString(36).slice(2)
    setNotifications((prev) => [...prev, { id, type, message, action }])
    const t = setTimeout(() => dismiss(id), action ? 8000 : 4000)
    timers.current.set(id, t)
  }, [dismiss])

  // Progress notifications have no auto-dismiss — they live until terminal WS state or manual dismiss.
  const notifyProgress = useCallback((jobId: string, message: string) => {
    const id = Math.random().toString(36).slice(2)
    setNotifications((prev) => [...prev, { id, type: 'progress', message, jobId }])
  }, [])

  return (
    <NotificationContext.Provider value={{ notify, notifyProgress, dismiss, notifications }}>
      {children}
    </NotificationContext.Provider>
  )
}

export function useNotification() {
  const ctx = useContext(NotificationContext)
  if (!ctx) throw new Error('useNotification must be used inside NotificationProvider')
  return ctx
}

// Re-export type alias for convenience
export type { NotificationContextValue }
