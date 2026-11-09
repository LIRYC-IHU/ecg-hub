import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Building2, Plus, Trash2, AlertTriangle } from 'lucide-react'
import { fetchTenants, createTenant, deleteTenant } from '../../lib/api'
import type { Tenant } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'
import { errorMessage } from '../../lib/errors'

// Same rule as the backend (and Keycloak organization aliases).
const SLUG_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

const inputClass =
  'w-full border border-border rounded px-2 py-1.5 text-sm bg-background focus:outline-none focus:ring-1 focus:ring-ring/20'

export function AdminTenantsPage() {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const { notify } = useNotification()

  const [showCreate, setShowCreate] = useState(false)
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const [deleting, setDeleting] = useState<Tenant | null>(null)
  const [confirmSlug, setConfirmSlug] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['admin', 'tenants'],
    queryFn: fetchTenants,
    staleTime: 15_000,
  })
  const tenants = data?.tenants ?? []

  const refresh = () => void queryClient.invalidateQueries({ queryKey: ['admin', 'tenants'] })

  const createMutation = useMutation({
    mutationFn: () => createTenant(slug.trim(), name.trim()),
    onSuccess: (tenant) => {
      refresh()
      setShowCreate(false)
      setSlug('')
      setName('')
      notify('success', t('admin.tenants.created', { slug: tenant.slug }))
    },
    onError: (err: unknown) => notify('error', errorMessage(err, t('admin.tenants.createError'))),
  })

  const deleteMutation = useMutation({
    mutationFn: (tenant: Tenant) => deleteTenant(tenant.id, confirmSlug),
    onSuccess: (res, tenant) => {
      refresh()
      setDeleting(null)
      setConfirmSlug('')
      notify(
        res.filesFailed > 0 ? 'warn' : 'success',
        t('admin.tenants.deleted', {
          slug: tenant.slug,
          ecgs: res.ecgsDeleted,
          files: res.filesRemoved,
          failed: res.filesFailed,
        }),
      )
    },
    onError: (err: unknown) => notify('error', errorMessage(err, t('admin.tenants.deleteError'))),
  })

  const slugValid = SLUG_RE.test(slug.trim())
  const fmtDate = (iso: string) => new Date(iso).toLocaleDateString(i18n.language)

  return (
    <div className="p-6">
      <div className="mb-6 flex items-start justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
            <Building2 className="h-5 w-5" />
          </div>
          <div>
            <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">{t('admin.tenants.title')}</h1>
            <p className="text-sm text-muted-foreground mt-0.5">{t('admin.tenants.subtitle')}</p>
          </div>
        </div>
        <button
          onClick={() => setShowCreate(true)}
          disabled={showCreate}
          className="flex items-center gap-1.5 text-xs bg-primary text-primary-foreground px-3 py-1.5 rounded-lg hover:bg-primary/90 disabled:opacity-50 transition-colors shrink-0"
        >
          <Plus className="w-3.5 h-3.5" />
          {t('admin.tenants.create')}
        </button>
      </div>

      {data && !data.multiTenantEnabled && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-foreground">
          <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0 text-amber-600" />
          <span>{t('admin.tenants.modeOff')}</span>
        </div>
      )}

      {showCreate && (
        <div className="mb-4 bg-card rounded-xl border border-border p-4 space-y-3 max-w-xl">
          <div>
            <label htmlFor="tenant-slug" className="block text-xs font-medium text-muted-foreground mb-1">
              {t('admin.tenants.slug')}
            </label>
            <input
              id="tenant-slug"
              autoFocus
              value={slug}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
              placeholder="cabinet-dupont"
              className={inputClass}
            />
            <p className={`text-xs mt-1 ${slug && !slugValid ? 'text-destructive' : 'text-muted-foreground'}`}>
              {t('admin.tenants.slugHelp')}
            </p>
          </div>
          <div>
            <label htmlFor="tenant-name" className="block text-xs font-medium text-muted-foreground mb-1">
              {t('admin.tenants.name')}
            </label>
            <input
              id="tenant-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Cabinet Dupont"
              className={inputClass}
            />
          </div>
          <div className="flex gap-2">
            <button
              onClick={() => createMutation.mutate()}
              disabled={!slugValid || !name.trim() || createMutation.isPending}
              className="flex items-center gap-1 text-xs bg-primary text-primary-foreground px-3 py-1.5 rounded hover:bg-primary/90 disabled:opacity-50 transition-colors"
            >
              {createMutation.isPending && <Spinner size={10} className="text-primary-foreground" />}
              {t('admin.tenants.create')}
            </button>
            <button
              onClick={() => { setShowCreate(false); setSlug(''); setName('') }}
              className="text-xs px-3 py-1.5 border border-border rounded hover:bg-muted/40 transition-colors text-muted-foreground"
            >
              {t('common.cancel')}
            </button>
          </div>
        </div>
      )}

      <div className="bg-card rounded-xl border border-border overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              <th className="px-4 py-2.5 font-medium">{t('admin.tenants.name')}</th>
              <th className="px-4 py-2.5 font-medium">{t('admin.tenants.slug')}</th>
              <th className="px-4 py-2.5 font-medium text-right">{t('admin.tenants.users')}</th>
              <th className="px-4 py-2.5 font-medium text-right">{t('admin.tenants.ecgs')}</th>
              <th className="px-4 py-2.5 font-medium">{t('admin.tenants.createdAt')}</th>
              <th className="px-4 py-2.5" />
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center">
                  <Spinner size={16} className="text-muted-foreground inline-block" />
                </td>
              </tr>
            )}
            {tenants.map((tenant) => (
              <tr key={tenant.id} className="border-b border-border last:border-0 align-top">
                <td className="px-4 py-3 text-foreground">
                  {tenant.name}
                  {tenant.isDefault && (
                    <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                      {t('admin.tenants.platform')}
                    </span>
                  )}
                </td>
                <td className="px-4 py-3 font-mono text-xs text-muted-foreground">{tenant.slug}</td>
                <td className="px-4 py-3 text-right tabular-nums">{tenant.userCount}</td>
                <td className="px-4 py-3 text-right tabular-nums">{tenant.ecgCount}</td>
                <td className="px-4 py-3 text-muted-foreground">{fmtDate(tenant.createdAt)}</td>
                <td className="px-4 py-3 text-right">
                  {!tenant.isDefault && deleting?.id !== tenant.id && (
                    <button
                      onClick={() => { setDeleting(tenant); setConfirmSlug('') }}
                      aria-label={t('admin.tenants.delete', { slug: tenant.slug })}
                      className="inline-flex items-center gap-1 text-xs text-destructive hover:bg-destructive/10 px-2 py-1 rounded transition-colors"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  )}
                  {deleting?.id === tenant.id && (
                    <div className="ml-auto w-72 space-y-2 text-left">
                      <p className="text-xs text-destructive">
                        {t('admin.tenants.deleteWarning', { ecgs: tenant.ecgCount, users: tenant.userCount })}
                      </p>
                      <label htmlFor="tenant-confirm" className="block text-xs text-muted-foreground">
                        {t('admin.tenants.typeSlug', { slug: tenant.slug })}
                      </label>
                      <input
                        id="tenant-confirm"
                        autoFocus
                        value={confirmSlug}
                        onChange={(e) => setConfirmSlug(e.target.value)}
                        className={inputClass}
                      />
                      <div className="flex gap-2">
                        <button
                          onClick={() => deleteMutation.mutate(tenant)}
                          disabled={confirmSlug !== tenant.slug || deleteMutation.isPending}
                          className="flex items-center gap-1 text-xs bg-destructive text-destructive-foreground px-3 py-1.5 rounded hover:bg-destructive/90 disabled:opacity-50 transition-colors"
                        >
                          {deleteMutation.isPending && <Spinner size={10} className="text-destructive-foreground" />}
                          {t('admin.tenants.deleteConfirm')}
                        </button>
                        <button
                          onClick={() => { setDeleting(null); setConfirmSlug('') }}
                          className="text-xs px-3 py-1.5 border border-border rounded hover:bg-muted/40 transition-colors text-muted-foreground"
                        >
                          {t('common.cancel')}
                        </button>
                      </div>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
