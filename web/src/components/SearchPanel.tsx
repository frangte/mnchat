import { useState, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, ApiError, type Channel, type Message, type SearchMode } from '../api'
import { dayLabel, formatTime } from '../format'
import { Avatar } from './Avatar'
import { Spinner } from './Spinner'

const TABS: { mode: SearchMode; label: string }[] = [
  { mode: 'messages', label: 'Messages' },
  { mode: 'threads', label: 'Threads' },
]

const FOCUS_RING = 'focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-indigo-500'

type Props = {
  ref: Ref<HTMLElement>
  workspaceId: number
  channels: Channel[]
  query: string
  onClose: () => void
  onOpenResult: (channelId: number, threadRootId: number | null) => void
}

export function SearchPanel({ ref, workspaceId, channels, query, onClose, onOpenResult }: Props) {
  const [mode, setMode] = useState<SearchMode>('messages')
  const search = useQuery({
    queryKey: ['search', workspaceId, mode, query],
    queryFn: () => api.search(workspaceId, query, mode),
    retry: false,
  })

  const channelName = (id: number) => channels.find((c) => c.id === id)?.name

  const onTabKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    setMode(mode === 'messages' ? 'threads' : 'messages')
    e.currentTarget.querySelector<HTMLElement>('[aria-selected="false"]')?.focus()
  }

  const results = () => {
    if (search.isPending) {
      return (
        <div className="flex justify-center py-6">
          <Spinner />
        </div>
      )
    }
    if (search.isError) {
      return search.error instanceof ApiError && search.error.status === 503 ? (
        <p className="px-5 py-4 text-sm text-zinc-500">Search is temporarily unavailable. Chat still works.</p>
      ) : (
        <p className="px-5 py-4 text-sm text-red-600">Couldn't search: {search.error.message}</p>
      )
    }

    const { data } = search
    // Optional chaining: the API encodes an empty Go slice as null.
    if (!data.items?.length) {
      return (
        <p className="break-words px-5 py-4 text-sm text-zinc-500">
          {data.mode === 'messages'
            ? `No messages match "${query}".`
            : 'No matching threads. Only summarized threads can be found here.'}
        </p>
      )
    }
    return (
      <ul>
        {data.mode === 'messages'
          ? data.items.map(({ message: m }) => (
              <Result
                key={m.id}
                message={m}
                channelName={channelName(m.channel_id)}
                showTime
                onClick={() => onOpenResult(m.channel_id, m.thread_root_id)}
              >
                <span className="line-clamp-3 whitespace-pre-wrap break-words text-[15px] leading-relaxed text-zinc-800">
                  {m.body}
                </span>
              </Result>
            ))
          : data.items.map(({ root, summary, stale }) => (
              <Result
                key={root.id}
                message={root}
                channelName={channelName(root.channel_id)}
                onClick={() => onOpenResult(root.channel_id, root.id)}
              >
                <span className="block whitespace-pre-wrap break-words text-[15px] leading-relaxed text-zinc-800">
                  {summary}
                </span>
                {stale && <span className="mt-0.5 block text-xs text-zinc-500">May be out of date</span>}
              </Result>
            ))}
      </ul>
    )
  }

  return (
    <aside
      ref={ref}
      aria-label="Search"
      className="fixed inset-0 z-30 flex flex-col bg-white md:static md:z-auto md:w-96 md:shrink-0 md:border-l md:border-zinc-200"
    >
      <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-zinc-200 px-4">
        <div className="min-w-0">
          <h2 className="font-semibold">Search</h2>
          <p className="truncate text-xs text-zinc-500">{query}</p>
        </div>
        <button
          onClick={onClose}
          aria-label="Close search"
          className="rounded-md px-2 py-1 text-lg text-zinc-500 hover:bg-zinc-100"
        >
          ✕
        </button>
      </header>

      <div
        role="tablist"
        aria-label="Search results"
        onKeyDown={onTabKeyDown}
        className="flex shrink-0 gap-1 border-b border-zinc-200 px-4 py-2"
      >
        {TABS.map((t) => (
          <button
            key={t.mode}
            id={`search-tab-${t.mode}`}
            role="tab"
            aria-selected={t.mode === mode}
            tabIndex={t.mode === mode ? 0 : -1}
            onClick={() => setMode(t.mode)}
            className={`rounded-md px-2 py-1 text-sm font-medium transition ${FOCUS_RING} ${
              t.mode === mode ? 'bg-indigo-50 text-indigo-600' : 'text-zinc-500 hover:bg-zinc-100'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div role="tabpanel" aria-labelledby={`search-tab-${mode}`} className="flex-1 overflow-y-auto pb-4">
        {results()}
      </div>
    </aside>
  )
}

type ResultProps = {
  message: Message
  channelName: string | undefined
  showTime?: boolean
  onClick: () => void
  children: ReactNode
}

function Result({ message: m, channelName, showTime, onClick, children }: ResultProps) {
  return (
    <li>
      <button
        onClick={onClick}
        className={`flex w-full gap-3 px-5 py-2 text-left hover:bg-zinc-50 ${FOCUS_RING}`}
      >
        <Avatar id={m.user_id} name={m.user_name} />
        <span className="min-w-0 flex-1">
          <span className="flex items-baseline gap-2">
            <span className="truncate text-sm font-semibold">{m.user_name}</span>
            {channelName && <span className="truncate text-xs text-zinc-500"># {channelName}</span>}
          </span>
          {showTime && (
            <time className="block text-xs text-zinc-400">
              {dayLabel(m.created_at)}, {formatTime(m.created_at)}
            </time>
          )}
          {children}
        </span>
      </button>
    </li>
  )
}
