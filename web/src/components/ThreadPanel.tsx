import { useQuery } from '@tanstack/react-query'
import { api, type Channel } from '../api'
import { useStickToBottom } from '../useStickToBottom'
import { Composer } from './Composer'
import { MessageRow, startsGroup } from './MessageRow'
import { Spinner } from './Spinner'

type Props = {
  workspaceId: number
  channel: Channel
  rootId: number
  currentUserId: number
  onClose: () => void
}

export function ThreadPanel({ workspaceId, channel, rootId, currentUserId, onClose }: Props) {
  // Same query as MessageList, so the root's reply count stays live.
  const messages = useQuery({
    queryKey: ['messages', channel.id],
    queryFn: () => api.messages(workspaceId, channel.id),
  })
  const replies = useQuery({
    queryKey: ['replies', rootId],
    queryFn: () => api.replies(workspaceId, rootId),
  })
  const items = replies.data ?? []
  const { scrollRef, onScroll } = useStickToBottom(items.at(-1), currentUserId)

  const root = messages.data?.find((m) => m.id === rootId)
  if (!root) return null

  return (
    <aside
      aria-label="Thread"
      className="fixed inset-0 z-30 flex flex-col bg-white md:static md:z-auto md:w-96 md:shrink-0 md:border-l md:border-zinc-200"
    >
      <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-zinc-200 px-4">
        <div className="min-w-0">
          <h2 className="font-semibold">Thread</h2>
          <p className="truncate text-xs text-zinc-500"># {channel.name}</p>
        </div>
        <button
          onClick={onClose}
          aria-label="Close thread"
          className="rounded-md px-2 py-1 text-lg text-zinc-500 hover:bg-zinc-100"
        >
          ✕
        </button>
      </header>

      <div ref={scrollRef} onScroll={onScroll} className="flex-1 overflow-y-auto pb-4">
        <MessageRow message={root} header />
        {root.reply_count > 0 && (
          <div className="my-2 flex items-center px-5" role="separator">
            <span className="mr-3 text-xs text-zinc-500">
              {root.reply_count} {root.reply_count === 1 ? 'reply' : 'replies'}
            </span>
            <div className="h-px flex-1 bg-zinc-200" />
          </div>
        )}
        {replies.isPending && (
          <div className="flex justify-center py-6">
            <Spinner />
          </div>
        )}
        {replies.isError && (
          <p className="px-5 py-4 text-sm text-red-600">Couldn't load replies: {replies.error.message}</p>
        )}
        {items.map((m, i) => (
          <MessageRow key={m.id} message={m} header={startsGroup(items[i - 1], m)} />
        ))}
      </div>

      <Composer workspaceId={workspaceId} channel={channel} threadRootId={rootId} />
    </aside>
  )
}
