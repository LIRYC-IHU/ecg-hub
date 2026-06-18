import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Users, Check, X } from 'lucide-react'
import { fetchUsers, setUserRole, fetchRoles } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'

const roleBadgeClass: Record<string, string> = {
  admin:  'bg-destructive/10 text-destructive',
  writer: 'bg-warning/10 text-warning',
  reader: 'bg-primary/10 text-primary',
  '':     'bg-muted text-muted-foreground',
}

export function AdminUsersPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { notify } = useNotification()
  const [pendingRole, setPendingRole] = useState<Record<string, string>>({})
  const [feedback, setFeedback] = useState<Record<string, 'ok' | 'err'>>({})

  const { data: users = [], isLoading, isError } = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: fetchUsers,
    staleTime: 30_000,
  })

  const { data: roles = [] } = useQuery({
    queryKey: ['admin', 'roles'],
    queryFn: fetchRoles,
    staleTime: 30_000,
  })

  const mutation = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      setUserRole(userId, role),
    onSuccess: (_, { userId, role }) => {
      setFeedback((f) => ({ ...f, [userId]: 'ok' }))
      void queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
      const user = users.find((u) => u.id === userId)
      notify('success', `${user?.username ?? userId} — rôle "${role}" appliqué`)
      setTimeout(() => setFeedback((f) => { const n = { ...f }; delete n[userId]; return n }), 2000)
    },
    onError: (_, { userId }) => {
      setFeedback((f) => ({ ...f, [userId]: 'err' }))
      const user = users.find((u) => u.id === userId)
      notify('error', `Impossible de modifier le rôle de ${user?.username ?? userId}`)
      setTimeout(() => setFeedback((f) => { const n = { ...f }; delete n[userId]; return n }), 3000)
    },
  })

  function handleRoleChange(userId: string, role: string) {
    setPendingRole((p) => ({ ...p, [userId]: role }))
  }

  function handleApply(userId: string) {
    const role = pendingRole[userId]
    if (!role) return
    mutation.mutate({ userId, role })
    setPendingRole((p) => { const n = { ...p }; delete n[userId]; return n })
  }

  return (
    <div className="max-w-4xl space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-primary/10 text-primary ring-1 ring-primary/20">
            <Users className="h-5 w-5" />
          </div>
          <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            {t('nav.users')}
          </h1>
        </div>
        <p className="text-xs text-muted-foreground">
          {t('admin.users.requires')} <code>OIDC_ADMIN_CLIENT_SECRET</code>
        </p>
      </div>

      {isError && (
        <div className="border border-warning/20 bg-warning/10 rounded-lg p-4 text-sm text-warning">
          {t('admin.users.errorBanner')}
        </div>
      )}

      <div className="border rounded-xl bg-card overflow-hidden shadow-sm">
        <table className="w-full text-sm">
          <thead className="bg-muted/30 border-b">
            <tr>
              <th className="text-left px-4 py-2 font-medium text-muted-foreground text-xs">{t('admin.users.colUser')}</th>
              <th className="text-left px-4 py-2 font-medium text-muted-foreground text-xs">{t('admin.users.colEmail')}</th>
              <th className="text-left px-4 py-2 font-medium text-muted-foreground text-xs">{t('admin.users.colCurrentRole')}</th>
              <th className="text-left px-4 py-2 font-medium text-muted-foreground text-xs">{t('admin.users.colChangeRole')}</th>
              <th className="px-4 py-2 w-24" />
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-sm text-muted-foreground">
                  {t('common.loading')}
                </td>
              </tr>
            )}
            {!isLoading && users.length === 0 && !isError && (
              <tr>
                <td colSpan={5} className="px-4 py-8 text-center text-sm text-muted-foreground">
                  {t('admin.users.noUsers')}
                </td>
              </tr>
            )}
            {users.map((user) => {
              const selected = pendingRole[user.id]
              const fb = feedback[user.id]
              return (
                <tr key={user.id} className="border-b last:border-0 hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-2.5 font-medium">{user.username}</td>
                  <td className="px-4 py-2.5 text-xs text-muted-foreground">{user.email ?? '—'}</td>
                  <td className="px-4 py-2.5">
                    <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${roleBadgeClass[user.ecg_hub_role] ?? roleBadgeClass['']}`}>
                      {user.ecg_hub_role || t('admin.users.noRole')}
                    </span>
                  </td>
                  <td className="px-4 py-2.5">
                    <select
                      value={selected ?? ''}
                      onChange={(e) => handleRoleChange(user.id, e.target.value)}
                      className="border rounded px-2 py-1 text-xs outline-none focus:ring-1 focus:ring-primary bg-card"
                    >
                      <option value="">{t('admin.users.selectRole')}</option>
                      {roles.map((r) => (
                        <option key={r.name} value={r.name}>{r.name}</option>
                      ))}
                    </select>
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    {selected && (
                      <button
                        onClick={() => handleApply(user.id)}
                        disabled={mutation.isPending}
                        className="text-xs px-2.5 py-1 rounded bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors flex items-center gap-1.5"
                      >
                        {mutation.isPending ? <Spinner size={11} className="text-primary-foreground" /> : null}
                        {t('admin.users.apply')}
                      </button>
                    )}
                    {fb === 'ok' && <Check className="inline w-3.5 h-3.5 text-success ml-2" />}
                    {fb === 'err' && <X className="inline w-3.5 h-3.5 text-destructive ml-2" />}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <div className="text-xs text-muted-foreground space-y-1 pt-2">
        <p><span className={`inline-block text-xs px-1.5 py-0.5 rounded font-medium mr-2 ${roleBadgeClass.reader}`}>reader</span>{t('admin.users.roleDesc.reader')}</p>
        <p><span className={`inline-block text-xs px-1.5 py-0.5 rounded font-medium mr-2 ${roleBadgeClass.writer}`}>writer</span>{t('admin.users.roleDesc.writer')}</p>
        <p><span className={`inline-block text-xs px-1.5 py-0.5 rounded font-medium mr-2 ${roleBadgeClass.admin}`}>admin</span>{t('admin.users.roleDesc.admin')}</p>
      </div>
    </div>
  )
}
