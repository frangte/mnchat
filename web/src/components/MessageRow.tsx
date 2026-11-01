import type { Message } from '../api'
import { formatTime, isSameDay } from '../format'
import { Avatar } from './Avatar'

const GROUP_WINDOW_MS = 5 * 60 * 1000

/** A message starts a new visual group when the author changes or 5+ minutes have passed. */
export function startsGroup(prev: Message | undefined, m: Message) {
  if (!prev || prev.user_id !== m.user_id || !isSameDay(prev.created_at, m.created_at)) return true
  return new Date(m.created_at).getTime() - new Date(prev.created_at).getTime() > GROUP_WINDOW_MS
}

type Props = { message: Message; header: boolean; onOpenThread?: (rootId: number) => void }

export function MessageRow({ message: m, header, onOpenThread }: Props) {
  return (
    <div className={`group relative flex gap-3 px-5 py-0.5 hover:bg-zinc-50 ${header ? 'mt-2 pt-1.5' : ''}`}>
      <div className="w-9 shrink-0">
        {header ? (
          <Avatar id={m.user_id} name={m.user_name} />
        ) : (
          <time className="invisible block pt-1 text-right text-[10px] text-zinc-400 group-hover:visible">
            {formatTime(m.created_at)}
          </time>
        )}
      </div>
      <div className="min-w-0 flex-1 [@media(hover:none)]:pr-14">
        {header && (
          <div className="flex items-baseline gap-2">
            <span className="text-sm font-semibold">{m.user_name}</span>
            <time className="text-xs text-zinc-400">{formatTime(m.created_at)}</time>
          </div>
        )}
        <p className="whitespace-pre-wrap break-words text-[15px] leading-relaxed text-zinc-800">{m.body}</p>
        {onOpenThread && m.reply_count > 0 && (
          <button
            onClick={() => onOpenThread(m.id)}
            className="mt-0.5 text-xs font-semibold text-indigo-600 hover:underline"
          >
            {m.reply_count} {m.reply_count === 1 ? 'reply' : 'replies'}
          </button>
        )}
      </div>
      {onOpenThread && (
        // Always visible without hover (touch screens), otherwise on hover or keyboard focus.
        <button
          onClick={() => onOpenThread(m.id)}
          aria-label={`Reply to ${m.user_name}`}
          className="absolute right-4 top-0 rounded-md border border-zinc-200 bg-white px-2 py-0.5 text-xs font-medium text-zinc-600 opacity-0 shadow-sm hover:bg-zinc-100 focus:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100"
        >
          Reply
        </button>
      )}
    </div>
  )
}
