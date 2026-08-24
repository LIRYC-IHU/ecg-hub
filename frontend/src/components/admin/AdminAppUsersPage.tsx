import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useState, useMemo } from 'react'
import { Search, Check, Trash2, Users, UserPlus } from 'lucide-react'
import { fetchAppUsers, setAppUserRole, deleteAppUser, fetchRoles, fetchUserDefaults, saveUserDefaults, createLocalUser, fetchAuthProviders } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'
import { useConfirm } from '../../context/ConfirmContext'
import { errorMessage } from '../../lib/errors';

const providerColors: Record<string, string> = {
  oidc:  'bg-primary/10 text-primary',
  ldap:  'bg-ldap/10 text-ldap',
  local: 'bg-muted text-muted-foreground',
}

const roleColors: Record<string, string> = {
  admin:  'bg-destructive/10 text-destructive',
  writer: 'bg-warning/10 text-warning',
  reader: 'bg-primary/10 text-primary',
}

export function AdminAppUsersPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { notify } = useNotification()
  const confirm = useConfirm()
  const [search, setSearch] = useState('')
  const [pendingRole, setPendingRole] = useState<Record<string, string>>({})
  const [feedback, setFeedback] = useState<Record<string, 'success' | 'error'>>({})
  const [pendingDefault, setPendingDefault] = useState<string | undefined>(undefined)
  const [newUser, setNewUser] = useState({ username: '', password: '', role: '' })

  const { data: users = [], isLoading, isError } = useQuery({
    queryKey: ['admin', 'app-users'],
    queryFn: fetchAppUsers,
    staleTime: 30_000,
  })

  const { data: roles = [] } = useQuery({
    queryKey: ['admin', 'roles'],
    queryFn: fetchRoles,
    staleTime: 30_000,
  })

  // The Add-user form only makes sense where passwords live in ECG Hub;
  // OIDC/LDAP sites provision accounts in their identity provider.
  const { data: providers = [] } = useQuery({
    queryKey: ['auth', 'providers'],
    queryFn: fetchAuthProviders,
    staleTime: 5 * 60_000,
  })
  const localEnabled = providers.includes('local')

  const { data: userDefaults } = useQuery({
    queryKey: ['admin', 'user-defaults'],
    queryFn: fetchUserDefaults,
    staleTime: 60_000,
  })

  const defaultRoleMutation = useMutation({
    mutationFn: (role: string) => saveUserDefaults(role),
    onSuccess: (_, role) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'user-defaults'] })
      notify('success', t('admin.appUsers.defaultRoleSaved', { role }))
      setPendingDefault(undefined)
    },
    onError: () => notify('error', t('common.error')),
  })

  const defaultRoleName = userDefaults?.default_role ?? 'reader'

  const createMutation = useMutation({
    mutationFn: () => createLocalUser(newUser.username.trim(), newUser.password, newUser.role || defaultRoleName),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'app-users'] })
      notify('success', t('admin.appUsers.created', { user: created.username }))
      setNewUser({ username: '', password: '', role: '' })
    },
    onError: (err: unknown) =>
      notify('error', errorMessage(err, t('common.error'))),
  })

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return users
    return users.filter(
      (u) =>
        u.external_id.toLowerCase().includes(q) ||
        u.provider.toLowerCase().includes(q) ||
        u.role_name.toLowerCase().includes(q),
    )
  }, [users, search])

  const mutation = useMutation({
    mutationFn: ({ id, role }: { id: string; role: string }) => setAppUserRole(id, role),
    onSuccess: (_, { id, role }) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'app-users'] })
      const user = users.find((u) => u.id === id)
      notify('success', `${user?.external_id ?? id} → "${role}"`)
      setPendingRole((p) => { const n = { ...p }; delete n[id]; return n })
      setFeedback((p) => ({ ...p, [id]: 'success' }))
      setTimeout(() => setFeedback((p) => { const n = { ...p }; delete n[id]; return n }), 2000)
    },
    onError: (_, { id }) => {
      notify('error', t('common.error'))
      setFeedback((p) => ({ ...p, [id]: 'error' }))
      setTimeout(() => setFeedback((p) => { const n = { ...p }; delete n[id]; return n }), 3000)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteAppUser(id),
    onSuccess: (_, id) => {
      void queryClient.invalidateQueries({ queryKey: ['admin', 'app-users'] })
      const user = users.find((u) => u.id === id)
      notify('success', t('admin.appUsers.deleted', { user: user?.external_id ?? id }))
    },
    onError: (err: unknown) => {
      const code = (err as { code?: string })?.code
      if (code === 'SELF_DELETE') notify('error', t('admin.appUsers.selfDelete'))
      else if (code === 'LAST_USER') notify('error', t('admin.appUsers.lastUser'))
      else notify('error', t('common.error'))
    },
  })

  async function handleDelete(user: { id: string; external_id: string }) {
    const ok = await confirm({
      title: t('common.delete'),
      message: t('admin.appUsers.confirmDelete', { user: user.external_id }),
      danger: true,
    })
    if (ok) deleteMutation.mutate(user.id)
  }

  function handleSave(userId: string) {
    const role = pendingRole[userId]
    if (!role && role !== '') return
    mutation.mutate({ id: userId, role })
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6 flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
          <Users className="h-5 w-5" />
        </div>
        <div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">{t('admin.appUsers.title')}</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{t('admin.appUsers.subtitle')}</p>
        </div>
      </div>

      {/* Default role for new users */}
      <div className="bg-card border border-border rounded-xl px-4 py-3 mb-5 flex items-center justify-between gap-4">
        <div>
          <p className="text-sm font-medium text-foreground">{t('admin.appUsers.defaultRole')}</p>
          <p className="text-xs text-muted-foreground mt-0.5">{t('admin.appUsers.defaultRoleHint')}</p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <select
            value={pendingDefault ?? userDefaults?.default_role ?? 'reader'}
            onChange={(e) => setPendingDefault(e.target.value)}
            className={`text-xs bg-card border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20 transition-colors ${
              pendingDefault !== undefined && pendingDefault !== userDefaults?.default_role
                ? 'border-primary'
                : 'border-border'
            }`}
          >
            {roles.map((r) => (
              <option key={r.name} value={r.name}>{r.name}</option>
            ))}
          </select>
          {pendingDefault !== undefined && pendingDefault !== userDefaults?.default_role && (
            <button
              onClick={() => defaultRoleMutation.mutate(pendingDefault)}
              disabled={defaultRoleMutation.isPending}
              className="text-xs bg-primary text-primary-foreground px-2.5 py-1 rounded font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 flex items-center gap-1"
            >
              {defaultRoleMutation.isPending && <Spinner size={11} className="text-primary-foreground" />}
              {t('common.save')}
            </button>
          )}
        </div>
      </div>

      {/* Add a local user — only where ECG Hub owns the credentials */}
      {localEnabled && (
        <form
          onSubmit={(e) => { e.preventDefault(); createMutation.mutate() }}
          className="bg-card border border-border rounded-xl px-4 py-3 mb-5"
        >
          <p className="text-sm font-medium text-foreground">{t('admin.appUsers.addTitle')}</p>
          <p className="text-xs text-muted-foreground mt-0.5 mb-3">{t('admin.appUsers.addHint')}</p>
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={newUser.username}
              onChange={(e) => setNewUser((u) => ({ ...u, username: e.target.value }))}
              placeholder={t('admin.appUsers.usernamePlaceholder')}
              autoComplete="off"
              className="flex-1 min-w-40 text-xs bg-background border border-border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20"
            />
            <input
              type="password"
              value={newUser.password}
              onChange={(e) => setNewUser((u) => ({ ...u, password: e.target.value }))}
              placeholder={t('admin.appUsers.passwordPlaceholder')}
              autoComplete="new-password"
              className="flex-1 min-w-40 text-xs bg-background border border-border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20"
            />
            <select
              value={newUser.role || defaultRoleName}
              onChange={(e) => setNewUser((u) => ({ ...u, role: e.target.value }))}
              className="text-xs bg-background border border-border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20"
            >
              {roles.map((r) => (
                <option key={r.name} value={r.name}>{r.name}</option>
              ))}
            </select>
            <button
              type="submit"
              disabled={createMutation.isPending || newUser.username.trim() === '' || newUser.password === ''}
              className="text-xs bg-primary text-primary-foreground px-3 py-1.5 rounded font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 flex items-center gap-1.5"
            >
              {createMutation.isPending ? <Spinner size={11} className="text-primary-foreground" /> : <UserPlus className="w-3.5 h-3.5" />}
              {t('admin.appUsers.addSubmit')}
            </button>
          </div>
        </form>
      )}

      {/* Search */}
      <div className="relative mb-4">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('admin.appUsers.searchPlaceholder')}
          className="w-full pl-10 pr-4 py-2.5 text-sm bg-card border border-border rounded-xl focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/50"
        />
      </div>

      {isError && (
        <div className="border border-warning/20 bg-warning/10 rounded-lg px-4 py-3 text-sm text-warning mb-4">
          {t('common.error')}
        </div>
      )}

      {/* Table */}
      <div className="bg-card rounded-xl border border-border overflow-hidden">
        {/* Header row */}
        <div className="grid grid-cols-[1fr_100px_110px_160px_90px] gap-4 px-4 py-2.5 bg-muted/50 border-b border-border">
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t('admin.appUsers.colUser')}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t('admin.appUsers.colProvider')}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t('admin.appUsers.colRole')}
          </span>
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
            {t('admin.users.colChangeRole')}
          </span>
          <span />
        </div>

        {/* Loading */}
        {isLoading && (
          <div className="flex items-center justify-center py-12">
            <Spinner size={18} className="text-muted-foreground" />
          </div>
        )}

        {/* Empty */}
        {!isLoading && !isError && filtered.length === 0 && (
          <div className="px-4 py-12 text-center text-sm text-muted-foreground">
            {search ? t('search.noResults') : t('admin.appUsers.noUsers')}
          </div>
        )}

        {/* Rows */}
        {filtered.map((user) => {
          const selected = pendingRole[user.id]
          const isDirty = selected !== undefined && selected !== user.role_name
          const isPending = mutation.isPending && mutation.variables?.id === user.id

          return (
            <div
              key={user.id}
              className="grid grid-cols-[1fr_100px_110px_160px_90px] gap-4 px-4 py-3 items-center border-b border-border last:border-0 hover:bg-muted/30 transition-colors"
            >
              {/* User */}
              <p className="text-sm font-medium text-foreground font-mono truncate">
                {user.external_id}
              </p>

              {/* Provider */}
              <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full w-fit ${providerColors[user.provider] ?? 'bg-muted text-muted-foreground'}`}>
                {user.provider.toUpperCase()}
              </span>

              {/* Current role + source */}
              <div className="flex flex-col items-start gap-0.5">
                {user.role_name ? (
                  <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full w-fit ${roleColors[user.role_name] ?? 'bg-muted text-muted-foreground'}`}>
                    {user.role_name}
                  </span>
                ) : (
                  <span className="text-[11px] text-muted-foreground">{t('admin.users.noRole')}</span>
                )}
                <span
                  className="text-[10px] text-muted-foreground"
                  title={t(user.role_manually_set ? 'admin.users.roleSource.manualHint' : 'admin.users.roleSource.autoHint')}
                >
                  {user.role_manually_set
                    ? t('admin.users.roleSource.manual')
                    : t('admin.users.roleSource.auto')}
                </span>
              </div>

              {/* Role selector */}
              <select
                value={selected ?? user.role_name ?? ''}
                onChange={(e) => setPendingRole((p) => ({ ...p, [user.id]: e.target.value }))}
                className={`text-xs bg-card border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20 transition-colors ${
                  isDirty ? 'border-primary' : 'border-border'
                }`}
              >
                {/* Placeholder only — a role is mandatory (ecg_hub_users.role_id
                    is NOT NULL), so it is never a selectable target. Shown only
                    when the user somehow has no role, so the select still has a
                    value to render. */}
                {!user.role_name && (
                  <option value="" disabled>{t('admin.users.noRole')}</option>
                )}
                {roles.map((r) => (
                  <option key={r.name} value={r.name}>{r.name}</option>
                ))}
              </select>

              {/* Apply + feedback */}
              <div className="flex items-center gap-1.5 justify-end">
                {isDirty && (
                  <button
                    onClick={() => handleSave(user.id)}
                    disabled={isPending}
                    className="text-xs bg-primary text-primary-foreground px-2.5 py-1 rounded font-medium hover:bg-primary/90 transition-colors disabled:opacity-50 flex items-center gap-1"
                  >
                    {isPending && <Spinner size={11} className="text-primary-foreground" />}
                    {t('admin.users.apply')}
                  </button>
                )}
                {feedback[user.id] === 'success' && (
                  <Check className="w-4 h-4 text-success" />
                )}
                <button
                  onClick={() => handleDelete(user)}
                  disabled={deleteMutation.isPending}
                  className="p-1.5 rounded-md text-destructive hover:bg-destructive/5 transition-colors"
                  title={t('admin.appUsers.delete')}
                  aria-label={t('admin.appUsers.delete')}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          )
        })}
      </div>

      <p className="text-[11px] text-muted-foreground mt-4">{t('admin.appUsers.oidcNote')}</p>
    </div>
  )
}
