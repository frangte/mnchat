import { Fragment } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Channel } from '../api'
import { dayLabel, isSameDay } from '../format'
import { useStickToBottom } from '../useStickToBottom'
import { MessageRow, startsGroup } from './MessageRow'
import { Spinner } from './Spinner'

type Props = {
  workspaceId: number
  channel: Channel
  currentUserId: number
  onOpenThread: (rootId: number) => void
}

export function MessageList({ workspaceId, channel, currentUserId, onOpenThread }: Props) {
  const messages = useQuery({
    queryKey: ['messages', channel.id],
    queryFn: () => api.messages(workspaceId, channel.id),
  })
  const items = messages.data ?? []
  const { scrollRef, onScroll } = useStickToBottom(items.at(-1), currentUserId)

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
            <MessageRow message={m} header={startsGroup(prev, m)} onOpenThread={onOpenThread} />
          </Fragment>
        )
      })}
    </div>
  )
}
