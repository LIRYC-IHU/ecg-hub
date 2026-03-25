import { useQuery } from '@tanstack/react-query'
import { fetchAdminStats, fetchHealth } from '../lib/api'

export function useAdminStats() {
  const stats = useQuery({
    queryKey: ['admin', 'stats'],
    queryFn: fetchAdminStats,
    staleTime: 30_000,
    refetchInterval: 60_000,
  })

  const health = useQuery({
    queryKey: ['health'],
    queryFn: fetchHealth,
    staleTime: 30_000,
    refetchInterval: 30_000,
  })

  return { stats, health }
}
