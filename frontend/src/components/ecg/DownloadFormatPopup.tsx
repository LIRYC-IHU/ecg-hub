import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { useQuery } from '@tanstack/react-query'
import { Download, X } from 'lucide-react'
import { fetchModules } from '../../lib/api'
import type { ExportFormat } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useDownloadFormatPrefs } from '../../hooks/useDownloadFormatPrefs'

interface Props {
  open: boolean
  onClose: () => void
  // Restrict the format list to a single vendor's module (per-ECG download).
  // When omitted, formats from every loaded module are offered (multi-ECG batch).
  vendor?: string
  busy?: boolean
  onConfirm: (formats: string[]) => void
}

// DownloadFormatPopup is a centered modal showing a checkbox list of available
// export formats. The user's selection is persisted via useDownloadFormatPrefs
// so the popup re-opens with the same checks across sessions.
export function DownloadFormatPopup({ open, onClose, vendor, busy, onConfirm }: Props) {
  const { t } = useTranslation()

  const { data: modules, isLoading } = useQuery({
    queryKey: ['modules'],
    queryFn: fetchModules,
    staleTime: 5 * 60_000,
    enabled: open,
  })

  const availableFormats = useMemo<ExportFormat[]>(() => {
    if (!modules) return []
    const list: ExportFormat[] = []
    const seen = new Set<string>()
    const sources = vendor ? modules.filter((m) => m.name === vendor) : modules
    for (const mod of sources) {
      for (const fmt of mod.formats ?? []) {
        if (!seen.has(fmt.id)) {
          seen.add(fmt.id)
          list.push(fmt)
        }
      }
    }
    return list
  }, [modules, vendor])

  const availableIds = availableFormats.map((f) => f.id)
  const { selected: persisted, save } = useDownloadFormatPrefs(availableIds)

  const [checked, setChecked] = useState<Set<string>>(() => new Set(persisted))

  // Re-sync the local checkbox state whenever the popup re-opens (persisted prefs
  // may have changed since the last mount).
  useEffect(() => {
    if (open) setChecked(new Set(persisted))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // Close on Escape — matches the dismiss affordance and keeps focus predictable.
  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  function toggle(id: string) {
    setChecked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function handleConfirm() {
    // Preserve the order returned by the API so the persisted pref stays stable.
    const ordered = availableIds.filter((id) => checked.has(id))
    if (ordered.length === 0) return
    save(ordered)
    onConfirm(ordered)
  }

  const hasSelection = checked.size > 0

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="bg-card border border-border rounded-lg p-5 w-full max-w-sm shadow-xl"
      >
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-sm font-semibold text-foreground">
            {t('ecg.downloadFormatsTitle')}
          </h2>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
            aria-label={t('common.close')}
          >
            <X size={14} />
          </button>
        </div>

        {isLoading || availableFormats.length === 0 ? (
          <div className="flex items-center gap-2 py-4 text-xs text-muted-foreground">
            <Spinner size={12} />
            {t('common.loading')}
          </div>
        ) : (
          <ul className="space-y-2 mb-4">
            {availableFormats.map((fmt) => (
              <li key={fmt.id}>
                <label className="flex items-center gap-2 text-sm cursor-pointer hover:bg-muted/40 rounded px-2 py-1.5 transition-colors">
                  <input
                    type="checkbox"
                    checked={checked.has(fmt.id)}
                    onChange={() => toggle(fmt.id)}
                    className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                  />
                  <span className="text-foreground">{fmt.label}</span>
                  {fmt.extension && (
                    <span className="text-[10px] font-mono text-muted-foreground ml-auto">
                      {fmt.extension}
                    </span>
                  )}
                </label>
              </li>
            ))}
          </ul>
        )}

        <div className="flex justify-end gap-2">
          <button
            onClick={onClose}
            disabled={busy}
            className="px-3 py-1.5 text-xs rounded border border-border hover:bg-muted transition-colors disabled:opacity-50"
          >
            {t('common.cancel')}
          </button>
          <button
            onClick={handleConfirm}
            disabled={!hasSelection || busy}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {busy ? <Spinner size={11} className="text-primary-foreground" /> : <Download size={12} />}
            {t('ecg.download')}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
