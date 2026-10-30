import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type Message, type User } from '../api'
import { useAuth } from '../auth'
import { useWorkspaceSocket } from '../useWorkspaceSocket'
import { Sidebar } from '../components/Sidebar'
import { MessageList } from '../components/MessageList'
import { Composer } from '../components/Composer'
import { Spinner } from '../components/Spinner'

export function ChatPage({ user }: { user: User }) {
  const { logout } = useAuth()
  const [workspaceId, setWorkspaceId] = useState<number | null>(null)
  const [channelId, setChannelId] = useState<number | null>(null)
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
    if (m.channel_id !== activeChannelRef.current && m.user_id !== user.id) {
      setUnread((u) => ({ ...u, [m.channel_id]: (u[m.channel_id] ?? 0) + 1 }))
    }
  })

  const selectChannel = (id: number) => {
    setChannelId(id)
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
    setUnread({})
    setSidebarOpen(false)
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
        </header>

        {channel ? (
          <>
            <MessageList key={channel.id} workspaceId={workspace.id} channel={channel} currentUserId={user.id} />
            <Composer key={`composer-${channel.id}`} workspaceId={workspace.id} channel={channel} />
          </>
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-zinc-500">
            No channels yet. Create one with + in the sidebar.
          </div>
        )}
      </main>
    </div>
  )
}
