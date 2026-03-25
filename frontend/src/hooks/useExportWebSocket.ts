import { useState, useEffect, useRef } from 'react'

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

export function useExportWebSocket(jobId: string | null): ExportWSState {
  const [state, setState] = useState<ExportWSState>(INITIAL_STATE)
  const retriesRef = useRef(0)

  useEffect(() => {
    if (!jobId) return

    retriesRef.current = 0
    let ws: WebSocket
    let cancelled = false

    function connect() {
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const url = `${protocol}//${window.location.host}/api/v1/exports/${jobId}/ws`
      ws = new WebSocket(url)

      ws.onmessage = (e) => {
        const msg = JSON.parse(e.data as string)
        setState({
          status: msg.status,
          processedCount: msg.processed_count ?? 0,
          ecgCount: msg.ecg_count ?? 0,
          percent: msg.percent ?? 0,
          downloadUrl: msg.download_url ?? null,
          error: msg.error ?? null,
        })
      }

      ws.onclose = (e) => {
        if (cancelled) return
        // Reconnect unless terminal state (code 1000 = NormalClosure) or retries exhausted.
        if (retriesRef.current < 3 && e.code !== 1000) {
          const delay = Math.pow(2, retriesRef.current) * 1000
          retriesRef.current++
          setTimeout(connect, delay)
        }
      }
    }

    connect()
    return () => {
      cancelled = true
      ws?.close()
    }
  }, [jobId])

  return state
}
