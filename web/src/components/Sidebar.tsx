import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type User, type Workspace } from '../api'
import type { SocketStatus } from '../useWorkspaceSocket'
import { Avatar } from './Avatar'

type Props = {
  user: User
  workspaces: Workspace[]
  workspace: Workspace
  channels: Channel[]
  activeChannelId: number | undefined
  unread: Record<number, number>
  status: SocketStatus
  className: string
  onSelectWorkspace: (id: number) => void
  onSelectChannel: (id: number) => void
  onLogout: () => void
}

const STATUS = {
  open: { dot: 'bg-emerald-400', label: 'Live' },
  connecting: { dot: 'bg-amber-400', label: 'Connecting…' },
  closed: { dot: 'bg-red-400', label: 'Reconnecting…' },
} satisfies Record<SocketStatus, { dot: string; label: string }>

export function Sidebar(props: Props) {
  const { user, workspaces, workspace, channels, activeChannelId, unread, status, className } = props

  return (
    <div className={`${className} h-full shrink-0`}>
      {/* Workspace rail: only shown when the user belongs to several tenants. */}
      {workspaces.length > 1 && (
        <nav aria-label="Workspaces" className="flex w-16 flex-col items-center gap-2 bg-slate-950 py-3">
          {workspaces.map((w) => (
            <button
              key={w.id}
              title={`${w.name} · ${w.org_name}`}
              onClick={() => props.onSelectWorkspace(w.id)}
              className={`flex size-10 items-center justify-center rounded-xl text-sm font-bold transition ${
                w.id === workspace.id
                  ? 'bg-white text-slate-900 ring-2 ring-indigo-400 ring-offset-2 ring-offset-slate-950'
                  : 'bg-slate-800 text-slate-300 hover:bg-slate-700'
              }`}
            >
              {w.name.slice(0, 2).toUpperCase()}
            </button>
          ))}
        </nav>
      )}

      <aside className="flex w-64 flex-col bg-slate-900 text-slate-300">
        <div className="border-b border-white/10 px-4 py-3">
          <div className="truncate font-semibold text-white">{workspace.name}</div>
          <div className="flex items-center gap-1.5 text-xs text-slate-400">
            <span className="truncate">{workspace.org_name}</span>
            <span aria-hidden>·</span>
            <span className={`size-2 rounded-full ${STATUS[status].dot}`} />
            <span>{STATUS[status].label}</span>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto px-2 py-3">
          <ChannelSection
            workspaceId={workspace.id}
            channels={channels}
            activeChannelId={activeChannelId}
            unread={unread}
            onSelectChannel={props.onSelectChannel}
          />
        </div>

        <div className="flex items-center gap-2 border-t border-white/10 px-3 py-3">
          <Avatar id={user.id} name={user.display_name} size="sm" />
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium text-white">{user.display_name}</div>
            <div className="truncate text-xs text-slate-400">{workspace.role}</div>
          </div>
          <button
            onClick={props.onLogout}
            className="rounded-md px-2 py-1 text-xs text-slate-400 hover:bg-white/10 hover:text-white"
          >
            Switch user
          </button>
        </div>
      </aside>
    </div>
  )
}

type SectionProps = Pick<Props, 'channels' | 'activeChannelId' | 'unread' | 'onSelectChannel'> & {
  workspaceId: number
}

function ChannelSection({ workspaceId, channels, activeChannelId, unread, onSelectChannel }: SectionProps) {
  const qc = useQueryClient()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')

  const create = useMutation({
    mutationFn: (n: string) => api.createChannel(workspaceId, n),
    onSuccess: (ch) => {
      qc.setQueryData<Channel[]>(['channels', workspaceId], (old) =>
        old && !old.some((c) => c.id === ch.id) ? [...old, ch].sort((a, b) => a.name.localeCompare(b.name)) : old,
      )
      setAdding(false)
      setName('')
      onSelectChannel(ch.id)
    },
  })

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (name.trim()) create.mutate(name)
  }

  return (
    <div>
      <div className="flex items-center justify-between px-2 pb-1">
        <span className="text-xs font-semibold uppercase tracking-wide text-slate-400">Channels</span>
        <button
          onClick={() => {
            setAdding((v) => !v)
            create.reset()
          }}
          aria-label="Create channel"
          className="flex size-6 items-center justify-center rounded-md text-lg leading-none text-slate-400 hover:bg-white/10 hover:text-white"
        >
          +
        </button>
      </div>

      {adding && (
        <form onSubmit={submit} className="px-1 pb-2">
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setAdding(false)}
            placeholder="new-channel"
            className="w-full rounded-md border border-white/10 bg-slate-800 px-2 py-1.5 text-sm text-white outline-none placeholder:text-slate-500 focus:border-indigo-400"
          />
          {create.isError && <p className="mt-1 px-1 text-xs text-red-300">{create.error.message}</p>}
        </form>
      )}

      <ul>
        {channels.map((c) => {
          const active = c.id === activeChannelId
          const count = unread[c.id] ?? 0
          return (
            <li key={c.id}>
              <button
                onClick={() => onSelectChannel(c.id)}
                aria-current={active ? 'page' : undefined}
                className={`flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-[15px] transition ${
                  active
                    ? 'bg-indigo-600 text-white'
                    : count > 0
                      ? 'font-semibold text-white hover:bg-white/10'
                      : 'hover:bg-white/10'
                }`}
              >
                <span className={active ? 'text-indigo-200' : 'text-slate-500'}>#</span>
                <span className="flex-1 truncate">{c.name}</span>
                {count > 0 && (
                  <span className="rounded-full bg-rose-500 px-1.5 text-xs font-semibold text-white">{count}</span>
                )}
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
