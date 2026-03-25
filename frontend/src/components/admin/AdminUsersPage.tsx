import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchUsers, setUserRole, fetchRoles } from '../../lib/api'
import { Spinner } from '../ui/Spinner'
import { useNotification } from '../../context/NotificationContext'

const roleBadgeClass: Record<string, string> = {
  admin:  'bg-red-50 text-red-700',
  writer: 'bg-orange-50 text-orange-700',
  reader: 'bg-blue-50 text-blue-700',
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
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t('nav.users')}</h1>
        <p className="text-xs text-muted-foreground">
          {t('admin.users.requires')} <code>OIDC_ADMIN_CLIENT_SECRET</code>
        </p>
      </div>

      {isError && (
        <div className="border border-orange-200 bg-orange-50 rounded-lg p-4 text-sm text-orange-800">
          {t('admin.users.errorBanner')}
        </div>
      )}

      <div className="border rounded-lg bg-white overflow-hidden shadow-sm">
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
                <tr key={user.id} className="border-b last:border-0 hover:bg-muted/10">
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
                      className="border rounded px-2 py-1 text-xs outline-none focus:ring-1 focus:ring-primary bg-white"
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
                        className="text-xs px-2.5 py-1 rounded bg-primary text-white hover:bg-primary/90 disabled:opacity-50 transition-colors flex items-center gap-1.5"
                      >
                        {mutation.isPending ? <Spinner size={11} className="text-white" /> : null}
                        {t('admin.users.apply')}
                      </button>
                    )}
                    {fb === 'ok' && <span className="text-xs text-green-600 ml-2">✓</span>}
                    {fb === 'err' && <span className="text-xs text-red-600 ml-2">✗</span>}
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
