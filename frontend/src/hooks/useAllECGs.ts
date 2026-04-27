import { useQuery } from '@tanstack/react-query'
import { fetchAllECGs, type AllECGFilters } from '../lib/api'
import type { ECGWithPatient } from '../types'

export function useAllECGs(filters: AllECGFilters = {}): {
  ecgs: ECGWithPatient[]
  total: number
  isLoading: boolean
  isError: boolean
} {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['ecgs', 'timeline', filters],
    queryFn: () => fetchAllECGs(filters),
    staleTime: 30_000,
  })

  return {
    ecgs: data?.data ?? [],
    total: data?.total ?? 0,
    isLoading,
    isError,
  }
}
