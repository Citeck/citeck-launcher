import { useEffect, useState, useCallback, useRef } from 'react'
import { getDaemonLogs, API_BASE } from '../lib/api'
import { useTranslation } from '../lib/i18n'

interface DaemonLogsViewerProps {
  compact?: boolean
  active?: boolean
}

const MAX_LOG_SIZE = 500_000 // characters

export function DaemonLogsViewer({ compact = false, active = true }: DaemonLogsViewerProps) {
  const { t } = useTranslation()
  const [logs, setLogs] = useState('')
  const [error, setError] = useState<string | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const settleTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const activeRef = useRef(active)
  useEffect(() => { activeRef.current = active }, [active])
  const preRef = useRef<HTMLPreElement>(null)

  const fetchInitial = useCallback(() => {
    getDaemonLogs(500)
      .then((data) => { setLogs(data); setError(null) })
      .catch((e) => setError(e.message))
  }, [])

  // Initial fetch + streaming follow
  // Streaming follow — the endpoint replays last `tail` lines then streams new
  // data. The tail may arrive across multiple read() calls (TLS/network splits),
  // so the client can't treat the first read as "the tail". Instead we
  // accumulate into initialBuf during a settle window; once the window fires
  // (or another chunk arrives past the window), we commit the buffer as the
  // initial state. All subsequent chunks are deltas appended to state.
  useEffect(() => {
    if (!active) return

    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller

    const initialSettleMs = 400

    const startStream = async () => {
      try {
        const res = await fetch(`${API_BASE}/daemon/logs?follow=true&tail=500`, {
          signal: controller.signal,
        })
        if (!res.ok || !res.body) {
          // Fallback to one-shot REST fetch if streaming not supported
          fetchInitial()
          return
        }

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let initialBuf = ''
        let initialCommitted = false

        const commitInitial = () => {
          if (initialCommitted) return
          initialCommitted = true
          if (settleTimerRef.current) {
            clearTimeout(settleTimerRef.current)
            settleTimerRef.current = null
          }
          setLogs(initialBuf.length > MAX_LOG_SIZE ? initialBuf.slice(-MAX_LOG_SIZE) : initialBuf)
          initialBuf = ''
        }

        while (true) {
          const { done, value } = await reader.read()
          if (done) {
            commitInitial()
            break
          }
          const chunk = decoder.decode(value, { stream: true })
          if (!initialCommitted) {
            initialBuf += chunk
            if (settleTimerRef.current) clearTimeout(settleTimerRef.current)
            settleTimerRef.current = setTimeout(commitInitial, initialSettleMs)
          } else {
            setLogs(prev => {
              const merged = prev + chunk
              return merged.length > MAX_LOG_SIZE ? merged.slice(-MAX_LOG_SIZE) : merged
            })
          }
        }
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        retryTimerRef.current = setTimeout(() => {
          retryTimerRef.current = null
          if (activeRef.current) startStream()
        }, 3000)
      }
    }

    startStream()

    return () => {
      controller.abort()
      if (retryTimerRef.current) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
      if (settleTimerRef.current) {
        clearTimeout(settleTimerRef.current)
        settleTimerRef.current = null
      }
    }
  }, [active, fetchInitial])

  // Auto-scroll to bottom on new content
  useEffect(() => {
    if (preRef.current) {
      preRef.current.scrollTop = preRef.current.scrollHeight
    }
  }, [logs])

  return (
    <div className={compact ? 'flex flex-col h-full px-2 py-1' : 'p-3 flex flex-col h-full'}>
      <div className="flex items-center justify-between mb-1 shrink-0">
        <h2 className={compact ? 'text-xs font-medium' : 'text-base font-semibold'}>{t('daemonLogs.title')}</h2>
        <button type="button" className="rounded border border-border px-2 py-0.5 text-xs hover:bg-muted"
          onClick={fetchInitial}>{t('daemonLogs.refresh')}</button>
      </div>
      {error && <div className="text-destructive text-xs mb-1 shrink-0">{error}</div>}
      <pre ref={preRef} className="flex-1 min-h-0 overflow-auto rounded border border-border bg-background p-2 text-[11px] font-mono text-foreground whitespace-pre-wrap">
        {logs || t('daemonLogs.empty')}
      </pre>
    </div>
  )
}
