import { useQuery } from '@tanstack/react-query'
import { fetchPatients } from '../lib/api'
import type { PatientFilters } from '../lib/api'
import type { Patient } from '../types'

export function usePatients(filters: PatientFilters): {
  patients: Patient[]
  total: number
  isLoading: boolean
  isError: boolean
  error: unknown
} {
  const { data, isLoading, isError, error } = useQuery({
    // Structured array key — MANDATORY per architecture (TanStack Query v5)
    queryKey: ['patients', filters],
    queryFn: () => fetchPatients(filters),
    staleTime: 30_000,
  })

  return {
    patients: data?.data ?? [],
    total: data?.total ?? 0,
    isLoading,
    isError,
    error,
  }
}
