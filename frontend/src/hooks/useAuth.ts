import { useState, useEffect, useCallback } from 'react'
import { fetchMe, type MeResponse } from '../lib/api'

export type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated'

export function useAuth() {
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [user, setUser] = useState<MeResponse | null>(null)

  useEffect(() => {
    fetchMe()
      .then((u) => {
        setUser(u)
        setStatus('authenticated')
      })
      .catch(() => {
        setStatus('unauthenticated')
      })
  }, [])

  const hasPermission = useCallback((permission: string): boolean => {
    return user?.permissions?.includes(permission) ?? false
  }, [user])

  function logout() {
    window.location.href = '/api/v1/auth/logout'
  }

  return { status, user, logout, hasPermission }
}
