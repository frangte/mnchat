import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getToken, socketURL, type Message, type ServerEvent } from './api'

export type SocketStatus = 'connecting' | 'open' | 'closed'

/** Adds m to a channel's cached messages unless it is already there (REST + WS both deliver it). */
export function appendMessage(old: Message[] | undefined, m: Message) {
  if (!old || old.some((x) => x.id === m.id)) return old
  return [...old, m]
}

/**
 * Keeps one WebSocket per workspace and writes incoming events into the
 * react-query cache. Reconnects with backoff and refetches anything missed.
 */
export function useWorkspaceSocket(workspaceId: number | undefined, onMessage?: (m: Message) => void) {
  const qc = useQueryClient()
  const [status, setStatus] = useState<SocketStatus>('connecting')
  const onMessageRef = useRef(onMessage)
  useEffect(() => {
    onMessageRef.current = onMessage
  })

  useEffect(() => {
    const token = getToken()
    if (!workspaceId || !token) return

    let socket: WebSocket | null = null
    let retry = 0
    let timer: number | undefined
    let stopped = false

    const connect = () => {
      setStatus('connecting')
      socket = new WebSocket(socketURL(workspaceId, token))
      socket.onopen = () => {
        if (retry > 0) {
          qc.invalidateQueries({ queryKey: ['messages'] })
          qc.invalidateQueries({ queryKey: ['channels', workspaceId] })
        }
        retry = 0
        setStatus('open')
      }
      socket.onmessage = (e) => {
        const ev = JSON.parse(e.data) as ServerEvent
        switch (ev.type) {
          case 'message.created':
            qc.setQueryData<Message[]>(['messages', ev.channel_id], (old) => appendMessage(old, ev.data))
            onMessageRef.current?.(ev.data)
            break
          case 'channel.created':
            qc.invalidateQueries({ queryKey: ['channels', workspaceId] })
            break
        }
      }
      socket.onclose = () => {
        if (stopped) return
        setStatus('closed')
        const delay = Math.min(1000 * 2 ** retry, 15000)
        retry++
        timer = window.setTimeout(connect, delay)
      }
    }

    connect()
    return () => {
      stopped = true
      window.clearTimeout(timer)
      socket?.close()
    }
  }, [workspaceId, qc])

  return status
}
