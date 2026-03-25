import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, FileText, RefreshCw, Trash2, ChevronDown, ChevronUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { downloadECG, downloadECGXMLFDA, fetchECGMeta, forceHL7, deleteECG, patchECGMetadata } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'
import type { ECG, ECGFieldDef } from '../../types'

const XMLFDA_SUPPORTED_VENDORS = ['philips']

const hl7StatusConfig: Record<string, { dot: string; label: string }> = {
  success:      { dot: 'bg-success',          label: 'Envoyé'     },
  pending:      { dot: 'bg-muted-foreground',  label: 'En attente' },
  hl7_exhausted:{ dot: 'bg-warning',           label: 'Épuisé'     },
}

interface Props {
  ecg: ECG
  isSelected: boolean
  onToggle: () => void
  canForceHL7?: boolean
  canDelete?: boolean
  canRead?: boolean
  canWrite?: boolean
  patientId?: number
  onDeleted?: (ecgId: number) => void
}

export function EcgRow({ ecg, isSelected, onToggle, canForceHL7, canDelete, canRead, canWrite, patientId, onDeleted }: Props) {
  const { t } = useTranslation()
  const { notify } = useNotification()
  const queryClient = useQueryClient()
  const [forced, setForced] = useState(false)
  const [deleted, setDeleted] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [expanded, setExpanded] = useState(false)

  const date = new Date(ecg.recorded_at ?? ecg.ingested_at).toLocaleString('fr-FR', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })

  const forceMutation = useMutation({
    mutationFn: () => forceHL7(ecg.id),
    onSuccess: () => {
      setForced(true)
      void queryClient.invalidateQueries({ queryKey: ['ecgs', patientId] })
      notify('success', `ECG #${ecg.id} — retry HL7 planifié`)
    },
    onError: () => notify('error', `ECG #${ecg.id} — échec du retry HL7`),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteECG(ecg.id),
    onSuccess: () => {
      setDeleted(true)
      void queryClient.invalidateQueries({ queryKey: ['ecgs', patientId] })
      onDeleted?.(ecg.id)
      notify('success', `ECG supprimé — ${ecg.original_filename}`)
    },
    onError: () => {
      setConfirmDelete(false)
      notify('error', `Erreur lors de la suppression — ${ecg.original_filename}`)
    },
  })

  if (deleted) return null

  const hl7 = hl7StatusConfig[ecg.hl7_status] ?? { dot: 'bg-muted', label: ecg.hl7_status }

  return (
    <div className="border-b border-border last:border-0">
      {/* Main row */}
      <div
        className={`grid grid-cols-[32px_140px_80px_120px_1fr_auto] gap-3 px-6 py-2.5 items-center transition-colors ${
          isSelected ? 'bg-primary/5' : 'hover:bg-muted/20'
        }`}
      >
        {/* Checkbox */}
        <input
          type="checkbox"
          checked={isSelected}
          onChange={onToggle}
          onClick={(e) => e.stopPropagation()}
          className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
          aria-label={ecg.original_filename}
        />

        {/* Date */}
        <span className="text-xs font-mono text-muted-foreground">{date}</span>

        {/* Vendor — DICOM sources get a distinct cyan badge */}
        <span className={`text-[11px] px-2 py-0.5 rounded w-fit font-medium ${
          ecg.vendor === 'dicom'
            ? 'bg-cyan-500/10 text-cyan-700 dark:text-cyan-400'
            : 'bg-primary/5 text-primary/80'
        }`}>
          {ecg.vendor}
        </span>

        {/* HL7 status */}
        <div className="flex items-center gap-1.5">
          <div className={`w-2 h-2 rounded-full shrink-0 ${hl7.dot}`} />
          <span className="text-[11px] text-muted-foreground">{hl7.label}</span>
        </div>

        {/* Filename */}
        <span className="text-[11px] font-mono text-muted-foreground/70 truncate">
          {ecg.original_filename}
        </span>

        {/* Actions */}
        <div className="flex items-center gap-1 shrink-0">
          {/* Download original */}
          <button
            onClick={() => downloadECG(ecg.id)}
            className="p-1.5 rounded hover:bg-muted transition-colors"
            title={t('ecg.downloadOriginal')}
          >
            <Download className="w-3.5 h-3.5 text-muted-foreground" />
          </button>

          {/* Download XMLFDA */}
          {XMLFDA_SUPPORTED_VENDORS.includes(ecg.vendor) && (
            <button
              onClick={() => downloadECGXMLFDA(ecg.id)}
              className="p-1.5 rounded hover:bg-muted transition-colors"
              title={t('ecg.downloadXmlfda')}
            >
              <FileText className="w-3.5 h-3.5 text-muted-foreground" />
            </button>
          )}

          {/* Force HL7 */}
          {canForceHL7 && ecg.hl7_status === 'hl7_exhausted' && !forced && (
            <button
              onClick={() => forceMutation.mutate()}
              disabled={forceMutation.isPending}
              className="p-1.5 rounded hover:bg-warning/10 transition-colors disabled:opacity-50"
              title={t('admin.hl7.forceRetry')}
            >
              {forceMutation.isPending
                ? <Spinner size={12} className="text-warning" />
                : <RefreshCw className="w-3.5 h-3.5 text-warning" />
              }
            </button>
          )}
          {canForceHL7 && forced && (
            <span className="text-[11px] text-success px-1">✓</span>
          )}

          {/* Delete */}
          {canDelete && (
            confirmDelete ? (
              <>
                <button
                  onClick={() => deleteMutation.mutate()}
                  disabled={deleteMutation.isPending}
                  className="text-[10px] font-medium text-destructive hover:underline disabled:opacity-50 flex items-center gap-0.5 px-1"
                >
                  {deleteMutation.isPending && <Spinner size={10} />}
                  {t('common.confirm')}
                </button>
                <button
                  onClick={() => setConfirmDelete(false)}
                  className="text-[10px] text-muted-foreground hover:underline px-1"
                >
                  {t('common.cancel')}
                </button>
              </>
            ) : (
              <button
                onClick={() => setConfirmDelete(true)}
                className="p-1.5 rounded hover:bg-destructive/10 transition-colors"
                title={t('ecg.delete')}
              >
                <Trash2 className="w-3.5 h-3.5 text-destructive/60" />
              </button>
            )
          )}

          {/* Metadata toggle */}
          {canRead && (
            <button
              onClick={() => setExpanded((v) => !v)}
              className="p-1.5 rounded hover:bg-muted transition-colors"
              title={t('ecg.meta.title')}
            >
              {expanded
                ? <ChevronUp className="w-3.5 h-3.5 text-muted-foreground" />
                : <ChevronDown className="w-3.5 h-3.5 text-muted-foreground" />
              }
            </button>
          )}
        </div>
      </div>

      {/* Metadata panel */}
      {expanded && canRead && (
        <MetaPanel ecgId={ecg.id} canWrite={!!canWrite} />
      )}
    </div>
  )
}

interface MetaPanelProps {
  ecgId: number
  canWrite: boolean
}

function MetaPanel({ ecgId, canWrite }: MetaPanelProps) {
  const { t } = useTranslation()
  const { notify } = useNotification()
  const [edits, setEdits] = useState<Record<string, string>>({})

  const { data, isLoading, isError } = useQuery({
    queryKey: ['ecg-meta', ecgId],
    queryFn: () => fetchECGMeta(ecgId),
    staleTime: 30_000,
  })

  const saveMutation = useMutation({
    mutationFn: () => patchECGMetadata(ecgId, edits as Record<string, unknown>),
    onSuccess: () => {
      setEdits({})
      notify('success', t('ecg.meta.saved'))
    },
    onError: () => notify('error', t('ecg.meta.saveError')),
  })

  if (isLoading) return (
    <div className="bg-muted/30 border-t border-border px-8 py-3 flex items-center gap-2 text-xs text-muted-foreground">
      <Spinner size={12} /> {t('ecg.meta.loading')}
    </div>
  )

  if (isError || !data) return (
    <div className="bg-muted/30 border-t border-border px-8 py-3 text-xs text-destructive">
      {t('common.error')}
    </div>
  )

  const meta = data
  const isDirty = Object.keys(edits).length > 0

  function getValue(field: ECGFieldDef): string {
    if (field.key in edits) return edits[field.key]
    const v = meta.values[field.key]
    return v !== undefined && v !== null ? String(v) : ''
  }

  return (
    <div className="bg-muted/30 border-t border-border px-8 py-4">
      <h4 className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground mb-3">
        {t('ecg.meta.title')}
      </h4>
      <div className="grid grid-cols-3 gap-x-8 gap-y-3">
        {meta.fields.map((field) => (
          <div key={field.key}>
            <p className="text-[10px] uppercase tracking-wider text-muted-foreground mb-1">{field.label}</p>
            {canWrite ? (
              <FieldInput field={field} value={getValue(field)} onChange={(v) => setEdits((p) => ({ ...p, [field.key]: v }))} />
            ) : (
              <p className="text-xs text-foreground">{getValue(field) || '—'}</p>
            )}
          </div>
        ))}
      </div>
      {canWrite && (
        <div className="mt-4 flex items-center gap-3">
          <button
            onClick={() => saveMutation.mutate()}
            disabled={!isDirty || saveMutation.isPending}
            className="flex items-center gap-1.5 text-xs bg-primary text-primary-foreground px-3 py-1.5 rounded-lg hover:bg-primary/90 disabled:opacity-40 transition-colors"
          >
            {saveMutation.isPending && <Spinner size={11} className="text-white" />}
            {saveMutation.isPending ? t('ecg.meta.saving') : t('ecg.meta.save')}
          </button>
          {isDirty && !saveMutation.isPending && (
            <button onClick={() => setEdits({})} className="text-xs text-muted-foreground hover:text-foreground transition-colors">
              {t('common.cancel')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

interface FieldInputProps {
  field: ECGFieldDef
  value: string
  onChange: (val: string) => void
}

function FieldInput({ field, value, onChange }: FieldInputProps) {
  const baseClass = 'text-xs border border-border rounded px-1.5 py-0.5 bg-background focus:outline-none focus:ring-1 focus:ring-ring/20 w-full'

  if (field.type === 'select' && field.options) {
    return (
      <select value={value} onChange={(e) => onChange(e.target.value)} className={baseClass}>
        <option value="">—</option>
        {field.options.map((opt) => <option key={opt} value={opt}>{opt}</option>)}
      </select>
    )
  }

  if (field.type === 'datetime') {
    const localVal = value ? value.substring(0, 16) : ''
    return (
      <input
        type="datetime-local"
        value={localVal}
        onChange={(e) => onChange(e.target.value ? e.target.value + ':00Z' : '')}
        className={baseClass}
      />
    )
  }

  return (
    <input
      type={field.type === 'number' ? 'number' : 'text'}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={baseClass}
    />
  )
}
