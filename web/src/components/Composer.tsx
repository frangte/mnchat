import { useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type Channel, type Message } from '../api'
import { appendMessage } from '../useWorkspaceSocket'

type Props = { workspaceId: number; channel: Channel; threadRootId?: number }

export function Composer({ workspaceId, channel, threadRootId }: Props) {
  const qc = useQueryClient()
  const label = threadRootId ? 'Reply…' : `Message #${channel.name}`
  const [text, setText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const send = useMutation({
    mutationFn: (body: string) => api.postMessage(workspaceId, channel.id, body, threadRootId),
    onSuccess: (m) => {
      const key = threadRootId ? ['replies', threadRootId] : ['messages', channel.id]
      qc.setQueryData<Message[]>(key, (old) => appendMessage(old, m))
      if (!channel.is_member) qc.invalidateQueries({ queryKey: ['channels', workspaceId] })
    },
    onError: (err, body) => {
      setText(body) // give the draft back so nothing is lost
      setError(err.message)
    },
  })

  useEffect(() => inputRef.current?.focus(), [])

  // Grow with content up to ~8 lines.
  useLayoutEffect(() => {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [text])

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const body = text.trim()
    if (!body) return
    setText('')
    setError(null)
    send.mutate(body)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // isComposing: don't send mid-IME (Vietnamese, Japanese, ... input methods).
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  return (
    <form onSubmit={submit} className="shrink-0 px-4 pb-4">
      <div className="rounded-xl border border-zinc-300 bg-white transition focus-within:border-indigo-500 focus-within:ring-2 focus-within:ring-indigo-500/20">
        <textarea
          ref={inputRef}
          rows={1}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={label}
          aria-label={label}
          className="block w-full resize-none bg-transparent px-3 pt-3 text-[15px] outline-none placeholder:text-zinc-400"
        />
        <div className="flex items-center justify-between gap-2 px-3 pb-2 pt-1">
          <span className="hidden text-xs text-zinc-400 sm:inline">Enter to send · Shift + Enter for a new line</span>
          <button
            type="submit"
            disabled={!text.trim()}
            className="ml-auto rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:opacity-40"
          >
            Send
          </button>
        </div>
      </div>
      {error && <p className="mt-1.5 text-sm text-red-600">{error}</p>}
    </form>
  )
}
