import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { useState, useMemo } from 'react'
import { Search, Check } from 'lucide-react'
import { fetchAppUsers, setAppUserRole, fetchRoles } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'

const providerColors: Record<string, string> = {
  oidc:  'bg-primary/10 text-primary',
  ldap:  'bg-purple-50 text-purple-700',
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
  const [search, setSearch] = useState('')
  const [pendingRole, setPendingRole] = useState<Record<number, string>>({})
  const [feedback, setFeedback] = useState<Record<number, 'success' | 'error'>>({})

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
    mutationFn: ({ id, role }: { id: number; role: string }) => setAppUserRole(id, role),
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

  function handleSave(userId: number) {
    const role = pendingRole[userId]
    if (!role && role !== '') return
    mutation.mutate({ id: userId, role })
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6">
        <h1 className="text-lg font-semibold text-foreground">{t('admin.appUsers.title')}</h1>
        <p className="text-sm text-muted-foreground mt-1">{t('admin.appUsers.subtitle')}</p>
      </div>

      {/* Search */}
      <div className="relative mb-4">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('admin.appUsers.searchPlaceholder')}
          className="w-full pl-10 pr-4 py-2.5 text-sm bg-card border border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/20 placeholder:text-muted-foreground/50"
        />
      </div>

      {isError && (
        <div className="border border-orange-200 bg-orange-50 rounded-lg px-4 py-3 text-sm text-orange-800 mb-4">
          {t('common.error')}
        </div>
      )}

      {/* Table */}
      <div className="bg-card rounded-lg border border-border overflow-hidden">
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
              className="grid grid-cols-[1fr_100px_110px_160px_90px] gap-4 px-4 py-3 items-center border-b border-border last:border-0 hover:bg-muted/20 transition-colors"
            >
              {/* User */}
              <p className="text-sm font-medium text-foreground font-mono truncate">
                {user.external_id}
              </p>

              {/* Provider */}
              <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full w-fit ${providerColors[user.provider] ?? 'bg-muted text-muted-foreground'}`}>
                {user.provider.toUpperCase()}
              </span>

              {/* Current role */}
              {user.role_name ? (
                <span className={`text-[11px] font-medium px-2 py-0.5 rounded-full w-fit ${roleColors[user.role_name] ?? 'bg-muted text-muted-foreground'}`}>
                  {user.role_name}
                </span>
              ) : (
                <span className="text-[11px] text-muted-foreground">{t('admin.users.noRole')}</span>
              )}

              {/* Role selector */}
              <select
                value={selected ?? user.role_name ?? ''}
                onChange={(e) => setPendingRole((p) => ({ ...p, [user.id]: e.target.value }))}
                className={`text-xs bg-card border rounded px-2 py-1.5 focus:outline-none focus:ring-1 focus:ring-ring/20 transition-colors ${
                  isDirty ? 'border-primary' : 'border-border'
                }`}
              >
                <option value="">{t('admin.users.noRole')}</option>
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
                    {isPending && <Spinner size={11} className="text-white" />}
                    {t('admin.users.apply')}
                  </button>
                )}
                {feedback[user.id] === 'success' && (
                  <Check className="w-4 h-4 text-success" />
                )}
              </div>
            </div>
          )
        })}
      </div>

      <p className="text-[11px] text-muted-foreground mt-4">{t('admin.appUsers.oidcNote')}</p>
    </div>
  )
}
