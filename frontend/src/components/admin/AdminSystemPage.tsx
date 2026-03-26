import { useMutation, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  CheckCircle,
  AlertCircle,
  Webhook,
  Copy,
  Lock,
  Database,
  Server,
  Activity,
  Clock,
  AlertTriangle,
  FileType,
  Radio,
  HardDrive,
} from 'lucide-react'
import { useAdminStats } from '../../hooks/useAdminStats'
import { fetchWebhookStatus, fetchModules, testWebhook } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'

export function AdminSystemPage() {
  const { t } = useTranslation()
  const { stats, health } = useAdminStats()
  const { notify } = useNotification()

  const webhookQuery = useQuery({
    queryKey: ['admin', 'webhook'],
    queryFn: fetchWebhookStatus,
    staleTime: 60_000,
  })

  const modulesQuery = useQuery({
    queryKey: ['admin', 'modules'],
    queryFn: fetchModules,
    staleTime: 60_000,
  })

  const testMutation = useMutation({
    mutationFn: testWebhook,
    onSuccess: (data) => {
      if (data.success) notify('success', `Webhook OK — HTTP ${data.status_code}`)
      else notify('warn', `Webhook — réponse HTTP ${data.status_code ?? '?'}`)
    },
    onError: () => notify('error', 'Webhook — impossible de joindre le serveur distant'),
  })

  const isOperational = health.data?.status === 'ok' && health.data?.database === 'ok'

  const dicomEnabled = health.data?.dicom_enabled ?? false
  const dicomPort    = health.data?.dicom_port ?? 0
  const ftpEnabled   = health.data?.ftp_enabled ?? false
  const ftpPort      = health.data?.ftp_port ?? 0

  const services = [
    { name: 'Base de données', icon: Database, ok: health.data?.database === 'ok', label: undefined },
    { name: 'API REST',        icon: Server,   ok: health.data?.status === 'ok',   label: undefined },
    { name: 'Serveur FTP',     icon: HardDrive, ok: ftpEnabled,                    label: ftpEnabled   ? `:${ftpPort}`   : 'Désactivé' },
    { name: 'Serveur DICOM',   icon: Radio,    ok: dicomEnabled,                   label: dicomEnabled ? `:${dicomPort}` : 'Désactivé' },
  ]

  function copyToClipboard(text: string) {
    void navigator.clipboard.writeText(text)
    notify('success', 'URL copiée')
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h1 className="text-lg font-semibold text-foreground">{t('admin.system.title')}</h1>
        <p className="text-sm text-muted-foreground mt-1">Surveillance des services et statistiques</p>
      </div>

      <div className="grid grid-cols-3 gap-6">
        {/* Left column (2/3) */}
        <div className="col-span-2 space-y-6">

          {/* Health */}
          <div className="bg-card rounded-lg border border-border p-5">
            <div className="flex items-center gap-3 mb-4">
              <div className={`w-8 h-8 rounded-full flex items-center justify-center ${isOperational ? 'bg-success/10' : 'bg-destructive/10'}`}>
                {isOperational
                  ? <CheckCircle className="w-5 h-5 text-success" />
                  : <AlertCircle className="w-5 h-5 text-destructive" />
                }
              </div>
              <div>
                <h2 className="text-sm font-semibold text-foreground">
                  {isOperational ? t('admin.system.operational') : t('admin.system.degraded')}
                </h2>
                {health.isLoading && (
                  <p className="text-[11px] text-muted-foreground">{t('common.loading')}</p>
                )}
              </div>
            </div>

            {health.data && (
              <div className="space-y-4">
                {/* Infrastructure */}
                <div>
                  <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2 px-1">
                    Infrastructure
                  </p>
                  <div className="space-y-1">
                    {services.map((svc) => (
                      <div key={svc.name} className="flex items-center gap-3 px-3 py-2 rounded-lg bg-muted/30">
                        <svc.icon className="w-4 h-4 text-muted-foreground" />
                        <span className="text-sm text-foreground flex-1">{svc.name}</span>
                        {svc.label && (
                          <span className="text-[10px] font-mono text-muted-foreground">{svc.label}</span>
                        )}
                        <div className={`w-2 h-2 rounded-full ${svc.ok ? 'bg-success' : 'bg-muted-foreground'}`} />
                        <span className={`text-[11px] font-medium ${svc.ok ? 'text-success' : 'text-muted-foreground'}`}>
                          {svc.ok ? 'OK' : (svc.label === 'Désactivé' ? '—' : 'KO')}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Formats ECG */}
                {!modulesQuery.isLoading && modulesQuery.data && modulesQuery.data.length > 0 && (
                  <div>
                    <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground mb-2 px-1">
                      Formats ECG supportés
                    </p>
                    <div className="space-y-1">
                      {modulesQuery.data.map((m) => (
                        <div key={m.name} className="flex items-center gap-3 px-3 py-2 rounded-lg bg-muted/30">
                          <FileType className="w-4 h-4 text-muted-foreground" />
                          <span className="text-sm text-foreground flex-1 capitalize">{m.name}</span>
                          <span className="text-[10px] font-mono text-muted-foreground">{m.extensions.join(', ')}</span>
                          <div className={`w-2 h-2 rounded-full ${m.status === 'ok' ? 'bg-success' : 'bg-warning'}`} />
                          <span className={`text-[11px] font-medium ${m.status === 'ok' ? 'text-success' : 'text-warning'}`}>
                            {m.status === 'ok' ? 'OK' : 'ERR'}
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>

          {/* Webhook */}
          <div className="bg-card rounded-lg border border-border p-5">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                <Webhook className="w-5 h-5 text-muted-foreground" />
                <h2 className="text-sm font-semibold text-foreground">Webhook HL7</h2>
                {webhookQuery.data && (
                  <span className={`text-[10px] font-medium px-2 py-0.5 rounded-full ${
                    webhookQuery.data.enabled
                      ? 'bg-success/10 text-success'
                      : 'bg-muted text-muted-foreground'
                  }`}>
                    {webhookQuery.data.enabled ? 'Activé' : 'Désactivé'}
                  </span>
                )}
              </div>
              {webhookQuery.data?.enabled && (
                <button
                  onClick={() => testMutation.mutate()}
                  disabled={testMutation.isPending}
                  className="text-xs border border-border px-3 py-1.5 rounded-lg hover:bg-muted transition-colors disabled:opacity-50 flex items-center gap-1.5"
                >
                  {testMutation.isPending && <Spinner size={11} />}
                  {testMutation.isPending ? 'Test…' : 'Tester'}
                </button>
              )}
            </div>

            {webhookQuery.isLoading && (
              <p className="text-sm text-muted-foreground">{t('common.loading')}</p>
            )}

            {webhookQuery.data && (
              <div className="space-y-3">
                {webhookQuery.data.url && (
                  <div className="flex items-center gap-2">
                    <span className="text-[11px] text-muted-foreground w-12">URL</span>
                    <code className="text-xs font-mono bg-muted px-2 py-1 rounded flex-1 truncate">
                      {webhookQuery.data.url}
                    </code>
                    <button
                      onClick={() => copyToClipboard(webhookQuery.data!.url)}
                      className="p-1 hover:bg-muted rounded transition-colors"
                    >
                      <Copy className="w-3.5 h-3.5 text-muted-foreground" />
                    </button>
                  </div>
                )}
                <div className="flex items-center gap-2">
                  <span className="text-[11px] text-muted-foreground w-12">Secret</span>
                  <div className="flex items-center gap-1.5">
                    <Lock className={`w-3 h-3 ${webhookQuery.data.secret_configured ? 'text-success' : 'text-warning'}`} />
                    <span className={`text-xs ${webhookQuery.data.secret_configured ? 'text-success' : 'text-warning'}`}>
                      {webhookQuery.data.secret_configured ? 'Configuré' : 'Manquant'}
                    </span>
                  </div>
                </div>
                {testMutation.data && (
                  <div className="flex items-center gap-2 mt-2">
                    <span className="text-[11px] text-muted-foreground">Dernier test :</span>
                    <span className={`text-xs font-mono font-medium ${testMutation.data.success ? 'text-success' : 'text-destructive'}`}>
                      {testMutation.data.success
                        ? `HTTP ${testMutation.data.status_code}`
                        : `✗ ${testMutation.data.error ?? `HTTP ${testMutation.data.status_code}`}`
                      }
                    </span>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        {/* Right column (1/3) — stats */}
        <div className="space-y-4">
          {stats.isLoading && (
            <div className="flex justify-center py-8">
              <Spinner size={18} className="text-muted-foreground" />
            </div>
          )}
          {stats.data && (
            <>
              <StatCard label="Total ECGs"    value={stats.data.total_ecgs}      icon={Activity}      color="text-foreground" />
              <StatCard label="Envoyés HL7"   value={stats.data.hl7_success}     icon={CheckCircle}   color="text-success" />
              <StatCard label="En attente"    value={stats.data.hl7_pending}     icon={Clock}         color="text-warning" />
              <StatCard label="Épuisés"       value={stats.data.hl7_exhausted}   icon={AlertTriangle} color={stats.data.hl7_exhausted > 0 ? 'text-warning' : 'text-foreground'} />
              <StatCard
                label="En quarantaine"
                value={stats.data.quarantine_count ?? 0}
                icon={AlertTriangle}
                color={(stats.data.quarantine_count ?? 0) > 0 ? 'text-quarantine' : 'text-foreground'}
              />
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function StatCard({
  label,
  value,
  icon: Icon,
  color,
}: {
  label: string
  value: number
  icon: React.ElementType
  color: string
}) {
  return (
    <div className="bg-card rounded-lg border border-border p-4 flex items-center gap-4">
      <Icon className={`w-5 h-5 shrink-0 ${color}`} />
      <div>
        <p className="text-[11px] text-muted-foreground">{label}</p>
        <p className={`text-xl font-bold tabular-nums ${color}`}>{value.toLocaleString()}</p>
      </div>
    </div>
  )
}
