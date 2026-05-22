import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, Shield } from 'lucide-react'
import { fetchRoles, createRole, updateRole, deleteRole } from '../../lib/api'
import type { AppRole } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'

const PERMISSION_GROUPS = [
  { key: 'patient',     labelKey: 'admin.roles.group.patient',     permissions: ['patient.read'] },
  { key: 'ecg',         labelKey: 'admin.roles.group.ecg',         permissions: ['ecg.read', 'ecg.write', 'ecg.download', 'ecg.delete'] },
  { key: 'hl7',         labelKey: 'admin.roles.group.hl7',         permissions: ['ecg.force_hl7', 'hl7.config', 'hl7.bulk_retry'] },
  { key: 'tag',         labelKey: 'admin.roles.group.tag',         permissions: ['tag.create', 'tag.delete', 'tag.apply'] },
  { key: 'quarantine',  labelKey: 'admin.roles.group.quarantine',  permissions: ['quarantine.read', 'quarantine.delete'] },
  { key: 'admin',       labelKey: 'admin.roles.group.admin',       permissions: ['admin.users', 'admin.roles', 'admin.audit', 'admin.system', 'admin.auth_config'] },
  { key: 'swagger',     labelKey: 'admin.roles.group.swagger',     permissions: ['swagger.read'] },
]

export function AdminRolesPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { notify } = useNotification()

  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [pendingPerms, setPendingPerms] = useState<Record<number, string[]>>({})
  const [showCreate, setShowCreate] = useState(false)
  const [newRoleName, setNewRoleName] = useState('')
  const [newRoleDesc, setNewRoleDesc] = useState('')

  const { data: roles = [], isLoading } = useQuery({
    queryKey: ['admin', 'roles'],
    queryFn: fetchRoles,
    staleTime: 30_000,
  })

  const selectedRole = roles.find((r) => r.id === selectedId) ?? null

  function getPerms(role: AppRole): string[] {
    return pendingPerms[role.id] ?? role.permissions
  }

  function togglePerm(role: AppRole, perm: string) {
    const current = getPerms(role)
    const next = current.includes(perm) ? current.filter((p) => p !== perm) : [...current, perm]
    setPendingPerms((prev) => ({ ...prev, [role.id]: next }))
  }

  const updateMutation = useMutation({
    mutationFn: ({ id, description, permissions }: { id: number; description: string; permissions: string[] }) =>
      updateRole(id, description, permissions),
    onSuccess: (_, { id }) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'roles'] })
      setPendingPerms((prev) => { const n = { ...prev }; delete n[id]; return n })
      notify('success', t('admin.roles.saved'))
    },
    onError: (err: unknown) => {
      const code = (err as { code?: string })?.code
      notify('error', code === 'LAST_ADMIN_ROLE' ? t('admin.roles.errorLastAdminRole') : t('admin.roles.saveError'))
    },
  })

  const createMutation = useMutation({
    mutationFn: () => createRole(newRoleName.trim(), newRoleDesc.trim(), []),
    onSuccess: (role) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'roles'] })
      setShowCreate(false)
      setNewRoleName('')
      setNewRoleDesc('')
      setSelectedId(role.id)
      notify('success', t('admin.roles.created', { name: role.name }))
    },
    onError: () => notify('error', t('admin.roles.createError')),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteRole(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'roles'] })
      setSelectedId(null)
      notify('success', t('admin.roles.deleted'))
    },
    onError: (err: unknown) => {
      const code = (err as { code?: string })?.code
      notify('error', code === 'ROLE_HAS_USERS' ? t('admin.roles.errorHasUsers') : t('admin.roles.deleteError'))
    },
  })

  const isDirty = selectedRole ? pendingPerms[selectedRole.id] !== undefined : false

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6">
        <h1 className="text-lg font-semibold text-foreground">{t('admin.roles.title')}</h1>
        <p className="text-sm text-muted-foreground mt-1">{t('admin.roles.subtitle')}</p>
      </div>

      <div className="flex gap-6">
        {/* Role list */}
        <div className="w-56 shrink-0">
          <div className="bg-card rounded-lg border border-border overflow-hidden">
            {isLoading && (
              <div className="flex justify-center p-4">
                <Spinner size={16} className="text-muted-foreground" />
              </div>
            )}

            {roles.map((role) => (
              <button
                key={role.id}
                onClick={() => setSelectedId(role.id)}
                className={`w-full flex items-center gap-2 px-3 py-2.5 text-sm text-left border-b border-border transition-colors relative last:border-0 ${
                  selectedId === role.id
                    ? 'bg-primary/5 text-primary font-medium'
                    : 'text-foreground hover:bg-muted/50'
                }`}
              >
                {selectedId === role.id && (
                  <div className="absolute left-0 top-1/2 -translate-y-1/2 w-0.5 h-5 bg-primary rounded-r" />
                )}
                <Shield className="w-3.5 h-3.5 shrink-0" />
                <span className="flex-1">{role.name}</span>
              </button>
            ))}

            {/* Create inline form */}
            {showCreate ? (
              <div className="p-3 border-t border-border space-y-2">
                <input
                  autoFocus
                  value={newRoleName}
                  onChange={(e) => setNewRoleName(e.target.value)}
                  placeholder={t('admin.roles.namePlaceholder')}
                  className="w-full border border-border rounded px-2 py-1.5 text-xs bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                />
                <input
                  value={newRoleDesc}
                  onChange={(e) => setNewRoleDesc(e.target.value)}
                  placeholder={t('admin.roles.descPlaceholder')}
                  className="w-full border border-border rounded px-2 py-1.5 text-xs bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                />
                <div className="flex gap-2">
                  <button
                    onClick={() => createMutation.mutate()}
                    disabled={!newRoleName.trim() || createMutation.isPending}
                    className="flex-1 flex items-center justify-center gap-1 text-xs bg-primary text-primary-foreground px-2 py-1.5 rounded hover:bg-primary/90 disabled:opacity-50 transition-colors"
                  >
                    {createMutation.isPending && <Spinner size={10} className="text-primary-foreground" />}
                    {t('common.confirm')}
                  </button>
                  <button
                    onClick={() => { setShowCreate(false); setNewRoleName(''); setNewRoleDesc('') }}
                    className="text-xs px-2 py-1.5 border border-border rounded hover:bg-muted/40 transition-colors text-muted-foreground"
                  >
                    {t('common.cancel')}
                  </button>
                </div>
              </div>
            ) : (
              <button
                onClick={() => setShowCreate(true)}
                className="w-full flex items-center gap-2 px-3 py-2.5 text-sm text-muted-foreground hover:text-foreground hover:bg-muted/50 transition-colors border-t border-border"
              >
                <Plus className="w-3.5 h-3.5" />
                {t('admin.roles.create')}
              </button>
            )}
          </div>
        </div>

        {/* Permission matrix */}
        {selectedRole ? (
          <div className="flex-1 bg-card rounded-lg border border-border p-6">
            <div className="flex items-start justify-between mb-6">
              <div>
                <h2 className="text-base font-semibold text-foreground capitalize">{selectedRole.name}</h2>
                {selectedRole.description && (
                  <p className="text-sm text-muted-foreground mt-0.5">{selectedRole.description}</p>
                )}
              </div>
              <button
                onClick={() => {
                  if (confirm(t('admin.roles.deleteConfirm', { name: selectedRole.name }))) {
                    deleteMutation.mutate(selectedRole.id)
                  }
                }}
                disabled={deleteMutation.isPending}
                className="flex items-center gap-1.5 text-xs text-destructive border border-destructive/20 px-3 py-1.5 rounded-lg hover:bg-destructive/5 disabled:opacity-50 transition-colors"
              >
                {deleteMutation.isPending
                  ? <Spinner size={11} className="text-destructive" />
                  : <Trash2 className="w-3.5 h-3.5" />
                }
                {t('common.delete')}
              </button>
            </div>

            <div className="space-y-6">
              {PERMISSION_GROUPS.map((group) => (
                <div key={group.key}>
                  <h3 className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground mb-3">
                    {t(group.labelKey)}
                  </h3>
                  <div className="grid grid-cols-2 gap-2">
                    {group.permissions.map((perm) => (
                      <label
                        key={perm}
                        className="flex items-center gap-2.5 px-3 py-2 rounded-lg hover:bg-muted/50 transition-colors cursor-pointer"
                      >
                        <input
                          type="checkbox"
                          checked={getPerms(selectedRole).includes(perm)}
                          onChange={() => togglePerm(selectedRole, perm)}
                          className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
                        />
                        <span className="text-sm text-foreground">
                          {t(`admin.roles.perm.${perm.replace('.', '_')}`)}
                        </span>
                      </label>
                    ))}
                  </div>
                </div>
              ))}
            </div>

            {isDirty && (
              <div className="flex gap-3 mt-8 pt-4 border-t border-border">
                <button
                  onClick={() => {
                    const perms = getPerms(selectedRole)
                    if (perms.length === 0) { notify('error', t('admin.roles.errorNoPermission')); return }
                    updateMutation.mutate({ id: selectedRole.id, description: selectedRole.description, permissions: perms })
                  }}
                  disabled={updateMutation.isPending}
                  className="flex items-center gap-1.5 bg-primary text-primary-foreground text-sm font-medium px-4 py-2 rounded-lg hover:bg-primary/90 disabled:opacity-50 transition-colors"
                >
                  {updateMutation.isPending && <Spinner size={12} className="text-primary-foreground" />}
                  {t('common.confirm')}
                </button>
                <button
                  onClick={() => setPendingPerms((prev) => { const n = { ...prev }; delete n[selectedRole.id]; return n })}
                  className="text-sm text-muted-foreground hover:text-foreground transition-colors"
                >
                  {t('common.cancel')}
                </button>
              </div>
            )}
          </div>
        ) : (
          !isLoading && roles.length > 0 && (
            <div className="flex-1 bg-card rounded-lg border border-border flex items-center justify-center">
              <p className="text-sm text-muted-foreground">{t('admin.roles.selectRole')}</p>
            </div>
          )
        )}
      </div>
    </div>
  )
}
