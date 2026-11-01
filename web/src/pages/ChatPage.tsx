import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Message, type User } from '../api'
import { useAuth } from '../auth'
import { useWorkspaceSocket } from '../useWorkspaceSocket'
import { Sidebar } from '../components/Sidebar'
import { MessageList } from '../components/MessageList'
import { Composer } from '../components/Composer'
import { ThreadPanel } from '../components/ThreadPanel'
import { SearchPanel } from '../components/SearchPanel'
import { Spinner } from '../components/Spinner'

type Panel = { kind: 'thread'; rootId: number } | { kind: 'search'; query: string } | null

export function ChatPage({ user }: { user: User }) {
  const { logout } = useAuth()
  const [workspaceId, setWorkspaceId] = useState<number | null>(null)
  const [channelId, setChannelId] = useState<number | null>(null)
  const [panel, setPanel] = useState<Panel>(null)
  const [searchText, setSearchText] = useState('')
  const searchPanelRef = useRef<HTMLElement>(null)
  const [unread, setUnread] = useState<Record<number, number>>({})
  const [sidebarOpen, setSidebarOpen] = useState(false)

  const workspaces = useQuery({ queryKey: ['workspaces'], queryFn: api.workspaces })
  const workspace = workspaces.data?.find((w) => w.id === workspaceId) ?? workspaces.data?.[0]

  const channels = useQuery({
    queryKey: ['channels', workspace?.id],
    queryFn: () => api.channels(workspace!.id),
    enabled: !!workspace,
  })
  const channel = channels.data?.find((c) => c.id === channelId) ?? channels.data?.[0]

  const activeChannelRef = useRef(channel?.id)
  useEffect(() => {
    activeChannelRef.current = channel?.id
  }, [channel?.id])

  const status = useWorkspaceSocket(workspace?.id, (m: Message) => {
    if (m.thread_root_id === null && m.channel_id !== activeChannelRef.current && m.user_id !== user.id) {
      setUnread((u) => ({ ...u, [m.channel_id]: (u[m.channel_id] ?? 0) + 1 }))
    }
  })

  const selectChannel = (id: number) => {
    setChannelId(id)
    setPanel(null)
    setUnread((u) => {
      const next = { ...u }
      delete next[id]
      return next
    })
    setSidebarOpen(false)
  }

  const selectWorkspace = (id: number) => {
    setWorkspaceId(id)
    setChannelId(null)
    setPanel(null)
    setSearchText('')
    setUnread({})
    setSidebarOpen(false)
  }

  const submitSearch = (e: FormEvent) => {
    e.preventDefault()
    const query = searchText.trim()
    if (query) setPanel({ kind: 'search', query })
  }

  const closeSearch = () => {
    setSearchText('')
    setPanel(null)
  }

  const onSearchKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return
    const searching = panel?.kind === 'search'
    if (e.key === 'Escape') {
      if (searching) closeSearch()
      else setSearchText('')
    } else if (e.key === 'Tab' && !e.shiftKey && searching) {
      // The panel sits beside the input but follows the whole message list in DOM order.
      e.preventDefault()
      searchPanelRef.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus()
    }
  }

  // selectChannel clears the panel, so the thread has to be set after it.
  const openSearchResult = (id: number, threadRootId: number | null) => {
    selectChannel(id)
    if (threadRootId !== null) setPanel({ kind: 'thread', rootId: threadRootId })
  }

  if (workspaces.isPending || (workspace && channels.isPending)) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }

  if (!workspace) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-4 text-center">
        <p className="font-medium">You're not in any workspace yet.</p>
        <button onClick={logout} className="text-sm text-indigo-600 hover:underline">
          Switch user
        </button>
      </div>
    )
  }

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar
        user={user}
        workspaces={workspaces.data ?? []}
        workspace={workspace}
        channels={channels.data ?? []}
        activeChannelId={channel?.id}
        unread={unread}
        status={status}
        className={sidebarOpen ? 'fixed inset-y-0 left-0 z-30 flex' : 'hidden md:flex'}
        onSelectWorkspace={selectWorkspace}
        onSelectChannel={selectChannel}
        onLogout={logout}
      />
      {sidebarOpen && (
        <div aria-hidden className="fixed inset-0 z-20 bg-black/40 md:hidden" onClick={() => setSidebarOpen(false)} />
      )}

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-zinc-200 px-4">
          <button
            onClick={() => setSidebarOpen(true)}
            aria-label="Open sidebar"
            className="-ml-1 rounded-md px-2 py-1 text-lg text-zinc-500 hover:bg-zinc-100 md:hidden"
          >
            ☰
          </button>
          <h2 className="truncate font-semibold">{channel ? `# ${channel.name}` : workspace.name}</h2>
          <form
            role="search"
            onSubmit={submitSearch}
            className="ml-auto hidden w-56 min-w-0 rounded-xl border border-zinc-300 bg-white transition focus-within:border-indigo-500 focus-within:ring-2 focus-within:ring-indigo-500/20 sm:block"
          >
            <input
              value={searchText}
              onChange={(e) => setSearchText(e.target.value)}
              onKeyDown={onSearchKeyDown}
              placeholder="Search…"
              aria-label="Search"
              maxLength={200}
              autoComplete="off"
              className="block w-full bg-transparent px-3 py-1.5 text-sm outline-none placeholder:text-zinc-400"
            />
          </form>
        </header>

        {channel ? (
          <>
            <MessageList
              key={channel.id}
              workspaceId={workspace.id}
              channel={channel}
              currentUserId={user.id}
              onOpenThread={(rootId) => setPanel({ kind: 'thread', rootId })}
            />
            <Composer key={`composer-${channel.id}`} workspaceId={workspace.id} channel={channel} />
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-zinc-500">
            No channels yet. Create one with + in the sidebar.
          </div>
        )}
      </main>

      {channel && panel?.kind === 'thread' && (
        <ThreadPanel
          key={panel.rootId}
          workspaceId={workspace.id}
          channel={channel}
          rootId={panel.rootId}
          currentUserId={user.id}
          onClose={() => setPanel(null)}
        />
      )}
      {panel?.kind === 'search' && (
        <SearchPanel
          ref={searchPanelRef}
          workspaceId={workspace.id}
          channels={channels.data ?? []}
          query={panel.query}
          onClose={closeSearch}
          onOpenResult={openSearchResult}
        />
      )}
    </div>
  )
}
