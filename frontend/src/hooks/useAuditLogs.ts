import { useQuery } from '@tanstack/react-query'
import { fetchAuditLogs } from '../lib/api'
import type { AuditLogFilters } from '../lib/api'

export function useAuditLogs(filters: AuditLogFilters = {}) {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['audit-logs', filters],
    queryFn: () => fetchAuditLogs(filters),
    staleTime: 30_000,
  })

  return {
    logs: data?.data ?? [],
    total: data?.total ?? 0,
    isLoading,
    isError,
  }
}
