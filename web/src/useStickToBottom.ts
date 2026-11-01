import { useLayoutEffect, useRef } from 'react'
import type { Message } from './api'

/** Sticks a list to the bottom when new messages arrive, unless the user scrolled up to read history. */
export function useStickToBottom(last: Message | undefined, currentUserId: number) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el && (nearBottom.current || last?.user_id === currentUserId)) {
      el.scrollTop = el.scrollHeight
    }
  }, [last?.id, last?.user_id, currentUserId])

  const onScroll = () => {
    const el = scrollRef.current
    if (el) nearBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }

  return { scrollRef, onScroll }
}
