import { useState, useEffect } from 'react'
import { exportClient } from '../lib/grpc'

export interface ExportWSState {
  status: 'connecting' | 'queued' | 'processing' | 'complete' | 'failed'
  processedCount: number
  ecgCount: number
  percent: number
  downloadUrl: string | null
  error: string | null
}

const INITIAL_STATE: ExportWSState = {
  status: 'connecting',
  processedCount: 0,
  ecgCount: 0,
  percent: 0,
  downloadUrl: null,
  error: null,
}

// useExportWebSocket streams a batch export job's progress from the gRPC
// ExportService.WatchProgress server-stream (formerly the /exports/:id/ws
// WebSocket). The name is kept for its consumers. Reconnects with backoff on a
// premature stream end, but stops once the job reaches a terminal state.
export function useExportWebSocket(jobId: string | null): ExportWSState {
  const [state, setState] = useState<ExportWSState>(INITIAL_STATE)

  useEffect(() => {
    if (!jobId) return

    setState(INITIAL_STATE)
    const abort = new AbortController()
    let cancelled = false
    let terminal = false

    async function run() {
      let retries = 0
      while (!cancelled && !terminal) {
        try {
          for await (const p of exportClient.watchProgress(
            { jobId: jobId! },
            { signal: abort.signal },
          )) {
            retries = 0
            setState({
              status: p.status as ExportWSState['status'],
              processedCount: p.processedCount,
              ecgCount: p.ecgCount,
              percent: p.percent,
              downloadUrl: p.downloadUrl || null,
              error: p.error || null,
            })
            if (p.status === 'complete' || p.status === 'failed') terminal = true
          }
        } catch {
          // premature end — reconnect below (AbortError filtered by `cancelled`)
        }
        if (cancelled || terminal || retries >= 3) break
        const delay = Math.pow(2, retries) * 1000
        retries++
        await new Promise((r) => setTimeout(r, delay))
      }
    }

    void run()
    return () => {
      cancelled = true
      abort.abort()
    }
  }, [jobId])

  return state
}
