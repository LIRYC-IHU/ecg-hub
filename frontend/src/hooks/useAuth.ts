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

  // The mount check above is a snapshot: a token that expires while the page
  // stays open leaves it stale, so drop to the login screen as soon as the
  // server rejects a call (SESSION_EXPIRED_EVENT).
  //
  // Only listen while a session is actually established. That does two things:
  // a failed sign-in — unauthenticated too — cannot be reported as an expiry on
  // the login screen, and the listener unsubscribes the moment the status
  // changes, so the handler runs exactly once.
  //
  // Running it more than once loops: clearing the cache makes every mounted
  // query refetch, the authenticated ones answer 401, that fires this event
  // again, and the app parks on its loading screen because the setup-status
  // query is in flight on every pass.
  useEffect(() => {
    if (status !== 'authenticated') return
    const onExpired = () => {
      setStatus('unauthenticated')
      setUser(null)
      // Cached patient data outlives the component tree. Clearing it stops the
      // next sign-in — possibly a different clinician on a shared workstation —
      // from seeing the previous session's records flash on screen.
      queryClient.clear()
    }
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired)
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired)
  }, [status, queryClient])

  const hasPermission = useCallback((permission: string): boolean => {
    return user?.permissions?.includes(permission) ?? false
  }, [user])

  function logout() {
    window.location.href = '/api/v1/auth/logout'
  }

  return { status, user, logout, hasPermission }
}
