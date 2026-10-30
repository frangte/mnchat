import { Fragment, useLayoutEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Channel, type Message } from '../api'
import { dayLabel, formatTime, isSameDay } from '../format'
import { Avatar } from './Avatar'
import { Spinner } from './Spinner'

const GROUP_WINDOW_MS = 5 * 60 * 1000

/** A message starts a new visual group when the author changes or 5+ minutes have passed. */
function startsGroup(prev: Message | undefined, m: Message) {
  if (!prev || prev.user_id !== m.user_id || !isSameDay(prev.created_at, m.created_at)) return true
  return new Date(m.created_at).getTime() - new Date(prev.created_at).getTime() > GROUP_WINDOW_MS
}

type Props = { workspaceId: number; channel: Channel; currentUserId: number }

export function MessageList({ workspaceId, channel, currentUserId }: Props) {
  const messages = useQuery({
    queryKey: ['messages', channel.id],
    queryFn: () => api.messages(workspaceId, channel.id),
  })
  const scrollRef = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const items = messages.data ?? []
  const last = items.at(-1)

  // Stick to the bottom when new messages arrive, unless the user scrolled up to read history.
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

  if (messages.isPending) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (messages.isError) {
    return (
      <div className="flex flex-1 items-center justify-center text-sm text-red-600">
        Couldn't load messages: {messages.error.message}
      </div>
    )
  }

  return (
    <div ref={scrollRef} onScroll={onScroll} className="flex-1 overflow-y-auto pb-4">
      <div className="px-5 pb-4 pt-10">
        <div className="mb-3 flex size-12 items-center justify-center rounded-xl bg-indigo-50 text-2xl font-semibold text-indigo-600">
          #
        </div>
        <h3 className="text-xl font-semibold">Welcome to #{channel.name}</h3>
        <p className="mt-1 text-sm text-zinc-500">This is the very beginning of the #{channel.name} channel.</p>
      </div>

      {items.map((m, i) => {
        const prev = items[i - 1]
        const newDay = !prev || !isSameDay(prev.created_at, m.created_at)
        const header = startsGroup(prev, m)
        return (
          <Fragment key={m.id}>
            {newDay && (
              <div className="my-3 flex items-center px-5" role="separator">
                <div className="h-px flex-1 bg-zinc-200" />
                <span className="mx-3 rounded-full border border-zinc-200 bg-white px-3 py-0.5 text-xs font-medium text-zinc-500">
                  {dayLabel(m.created_at)}
                </span>
                <div className="h-px flex-1 bg-zinc-200" />
              </div>
            )}
            <div className={`group flex gap-3 px-5 py-0.5 hover:bg-zinc-50 ${header ? 'mt-2 pt-1.5' : ''}`}>
              <div className="w-9 shrink-0">
                {header ? (
                  <Avatar id={m.user_id} name={m.user_name} />
                ) : (
                  <time className="invisible block pt-1 text-right text-[10px] text-zinc-400 group-hover:visible">
                    {formatTime(m.created_at)}
                  </time>
                )}
              </div>
              <div className="min-w-0 flex-1">
                {header && (
                  <div className="flex items-baseline gap-2">
                    <span className="text-sm font-semibold">{m.user_name}</span>
                    <time className="text-xs text-zinc-400">{formatTime(m.created_at)}</time>
                  </div>
                )}
                <p className="whitespace-pre-wrap break-words text-[15px] leading-relaxed text-zinc-800">{m.body}</p>
              </div>
            </div>
          </Fragment>
        )
      })}
    </div>
  )
}
