import { useState, useEffect, useCallback } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { fetchMe, type MeResponse } from '../lib/api'
import { SESSION_EXPIRED_EVENT } from '../lib/grpc'

export type AuthStatus = 'loading' | 'authenticated' | 'unauthenticated'

export function useAuth() {
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [user, setUser] = useState<MeResponse | null>(null)
  const queryClient = useQueryClient()

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

  // The mount check above is a snapshot. A token that expires while the page
  // stays open leaves it stale, so drop to the login screen as soon as the
  // server rejects a call — see SESSION_EXPIRED_EVENT.
  useEffect(() => {
    const onExpired = () => {
      // Only from an established session. A failed sign-in is unauthenticated
      // too, and must not be reported as an expiry on the login screen itself.
      setStatus((prev) => (prev === 'authenticated' ? 'unauthenticated' : prev))
      setUser(null)
      // Cached patient data outlives the component tree. Clearing it stops the
      // next sign-in — possibly a different clinician on a shared workstation —
      // from seeing the previous session's records flash on screen.
      queryClient.clear()
    }
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired)
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired)
  }, [queryClient])

  const hasPermission = useCallback((permission: string): boolean => {
    return user?.permissions?.includes(permission) ?? false
  }, [user])

  function logout() {
    window.location.href = '/api/v1/auth/logout'
  }

  return { status, user, logout, hasPermission }
}
