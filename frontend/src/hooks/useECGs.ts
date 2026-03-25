import { useQuery } from '@tanstack/react-query'
import { fetchECGs, type ECGFilters } from '../lib/api'
import type { ECG } from '../types'

export function useECGs(
  patientId: number | null,
  filters: ECGFilters = {},
): {
  ecgs: ECG[]
  total: number
  isLoading: boolean
  isError: boolean
} {
  const { data, isLoading, isError } = useQuery({
    // Structured array key — MANDATORY per architecture (TanStack Query v5)
    queryKey: ['ecgs', { patientId, ...filters }],
    queryFn: () => fetchECGs(patientId!, filters),
    enabled: patientId !== null,
    staleTime: 30_000,
  })

  return {
    ecgs: data?.data ?? [],
    total: data?.total ?? 0,
    isLoading,
    isError,
  }
}
