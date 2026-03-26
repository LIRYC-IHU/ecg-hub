import { useEffect } from 'react'
import { X, Download, AlertCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useNotification, type NotifType, type Notification } from '../../context/NotificationContext'
import { useExportWebSocket } from '../../hooks/useExportWebSocket'

const styles: Record<Exclude<NotifType, 'progress'>, string> = {
  success: 'bg-success/10 border-success/20 text-success',
  info:    'bg-primary/10 border-primary/20 text-primary',
  warn:    'bg-warning/10 border-warning/20 text-warning',
  error:   'bg-destructive/10 border-destructive/20 text-destructive',
}

const icons: Record<Exclude<NotifType, 'progress'>, string> = {
  success: '✓',
  info:    'ℹ',
  warn:    '⚠',
  error:   '✗',
}

function ProgressNotificationItem({ n, onDismiss }: { n: Notification; onDismiss: () => void }) {
  const { t } = useTranslation()
  const { status, percent, processedCount, ecgCount, downloadUrl, error } = useExportWebSocket(n.jobId ?? null)

  // Auto-dismiss on failure after 5 s
  useEffect(() => {
    if (status === 'failed') {
      const timer = setTimeout(onDismiss, 5000)
      return () => clearTimeout(timer)
    }
  }, [status, onDismiss])

  const title =
    status === 'complete' ? t('export.overlayComplete') :
    status === 'failed'   ? t('export.overlayFailed') :
    n.message

  return (
    <div className="flex flex-col gap-2 px-3.5 py-2.5 rounded-lg border shadow-md text-sm bg-card border-border text-foreground pointer-events-auto animate-in slide-in-from-right-4">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium leading-snug">{title}</span>
        <button
          onClick={onDismiss}
          className="shrink-0 opacity-50 hover:opacity-100 transition-opacity"
          aria-label={t('export.dismiss')}
        >
          <X size={14} />
        </button>
      </div>

      {(status === 'connecting' || status === 'queued' || status === 'processing') && (
        <div className="space-y-1">
          <div className="h-1.5 bg-muted rounded-full overflow-hidden">
            <div
              className="h-full bg-primary transition-all duration-300 rounded-full"
              style={{ width: `${percent}%` }}
            />
          </div>
          <p className="text-xs text-muted-foreground text-right">
            {ecgCount > 0
              ? `${processedCount} / ${ecgCount} (${percent}%)`
              : t('export.preparing')}
          </p>
        </div>
      )}

      {status === 'complete' && downloadUrl && (
        <a
          href={downloadUrl}
          download
          onClick={onDismiss}
          className="flex items-center gap-2 w-full justify-center bg-primary text-primary-foreground text-xs font-medium px-3 py-1.5 rounded-lg hover:bg-primary/90 transition-colors"
        >
          <Download size={13} />
          {t('export.done')}
        </a>
      )}

      {status === 'failed' && (
        <div className="flex items-start gap-2 text-destructive text-xs">
          <AlertCircle size={13} className="shrink-0 mt-px" />
          <span>{error ?? t('export.error')}</span>
        </div>
      )}
    </div>
  )
}

export function NotificationContainer() {
  const { notifications, dismiss } = useNotification()

  if (notifications.length === 0) return null

  return (
    <div className="fixed top-4 right-4 z-50 flex flex-col gap-2 w-80 pointer-events-none">
      {notifications.map((n) =>
        n.type === 'progress' ? (
          <ProgressNotificationItem key={n.id} n={n} onDismiss={() => dismiss(n.id)} />
        ) : (
          <div
            key={n.id}
            className={`flex items-start gap-2.5 px-3.5 py-2.5 rounded-lg border shadow-md text-sm pointer-events-auto animate-in slide-in-from-right-4 ${styles[n.type]}`}
          >
            <span className="font-bold shrink-0 mt-px">{icons[n.type]}</span>
            <span className="flex-1 leading-snug">{n.message}</span>
            <button
              onClick={() => dismiss(n.id)}
              className="shrink-0 opacity-50 hover:opacity-100 transition-opacity mt-px"
              aria-label="Fermer"
            >
              <X size={14} />
            </button>
          </div>
        )
      )}
    </div>
  )
}
