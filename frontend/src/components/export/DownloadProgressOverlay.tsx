import { useTranslation } from 'react-i18next'
import { X, Download, AlertCircle } from 'lucide-react'
import { useExportWebSocket } from '../../hooks/useExportWebSocket'

interface Props {
  jobId: string | null
  onDismiss: () => void
}

export function DownloadProgressOverlay({ jobId, onDismiss }: Props) {
  const { t } = useTranslation()
  const { status, percent, downloadUrl, error } = useExportWebSocket(jobId)

  if (!jobId) return null

  return (
    <div className="fixed bottom-4 right-4 z-50 w-72 bg-card border border-border rounded-xl shadow-lg p-4">
      <div className="flex items-center justify-between mb-2">
        <span className="text-sm font-medium text-foreground">
          {status === 'complete'
            ? t('export.overlayComplete')
            : status === 'failed'
            ? t('export.overlayFailed')
            : t('export.overlayTitle')}
        </span>
        <button
          onClick={onDismiss}
          className="text-muted-foreground hover:text-foreground transition-colors"
          aria-label={t('export.dismiss')}
        >
          <X size={14} />
        </button>
      </div>

      {status === 'processing' || status === 'connecting' || status === 'queued' ? (
        <div className="space-y-1">
          <div className="h-2 bg-muted rounded-full overflow-hidden">
            <div
              className="h-full bg-primary transition-all duration-300 rounded-full"
              style={{ width: `${percent}%` }}
            />
          </div>
          <p className="text-xs text-muted-foreground text-right">
            {t('export.progress', { percent })}
          </p>
        </div>
      ) : status === 'complete' && downloadUrl ? (
        <a
          href={downloadUrl}
          download
          className="flex items-center gap-2 mt-1 w-full justify-center bg-primary text-primary-foreground text-sm font-medium px-4 py-2 rounded-lg hover:bg-primary/90 transition-colors"
        >
          <Download size={14} />
          {t('export.done')}
        </a>
      ) : status === 'failed' ? (
        <div className="flex items-start gap-2 mt-1 text-destructive text-xs">
          <AlertCircle size={14} className="shrink-0 mt-px" />
          <span>{error ?? t('export.error')}</span>
        </div>
      ) : null}
    </div>
  )
}
