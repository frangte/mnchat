import { useEffect, useRef } from 'react'
import { useIsMutating, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../api'
import { Spinner } from './Spinner'

const MIN_REPLIES = 3

type Props = { workspaceId: number; rootId: number; replyCount: number }

function errorText(err: Error) {
  return err instanceof ApiError && err.status === 503 ? 'Summary is temporarily unavailable.' : err.message
}

export function ThreadSummary({ workspaceId, rootId, replyCount }: Props) {
  const qc = useQueryClient()
  const queryKey = ['summary', rootId]
  const mutationKey = ['summarize', rootId]

  const summary = useQuery({
    queryKey,
    queryFn: () =>
      api.summary(workspaceId, rootId).catch((err) => {
        // 404 just means nobody has generated one yet.
        if (err instanceof ApiError && err.status === 404) return null
        throw err
      }),
    enabled: replyCount >= MIN_REPLIES,
    retry: false,
  })

  const summarize = useMutation({
    mutationKey,
    mutationFn: () => api.summarize(workspaceId, rootId),
    onSuccess: async (result) => {
      // A GET still in flight started before this write and would overwrite it.
      await qc.cancelQueries({ queryKey })
      qc.setQueryData(queryKey, result)
    },
  })

  // Read from the mutation cache, not useMutation state: that survives closing and reopening
  // the thread mid-generation, and updates before React re-renders the disabled button.
  const generating = useIsMutating({ mutationKey }) > 0
  const generate = () => {
    if (qc.isMutating({ mutationKey }) === 0) summarize.mutate()
  }

  // A new reply can make the summary stale, and only the server knows.
  const seenCount = useRef(replyCount)
  useEffect(() => {
    if (seenCount.current === replyCount) return
    seenCount.current = replyCount
    qc.invalidateQueries({ queryKey: ['summary', rootId] })
  }, [qc, replyCount, rootId])

  if (replyCount < MIN_REPLIES) return null

  const data = summary.data
  const error = generating ? null : (summarize.error ?? summary.error)

  const action = (
    <>
      <button
        type="button"
        onClick={generate}
        disabled={generating}
        className="inline-flex items-center gap-2 rounded-lg bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:opacity-70"
      >
        {generating ? (
          <>
            <span aria-hidden>
              <Spinner className="size-4" />
            </span>
            Summarizing…
          </>
        ) : data ? (
          'Update summary'
        ) : (
          'Summary'
        )}
      </button>
      {error && <p className="mt-1.5 text-sm text-red-600">{errorText(error)}</p>}
    </>
  )

  return (
    <div aria-live="polite">
      {data ? (
        <div className="mx-5 my-2 rounded-xl border border-zinc-200 bg-zinc-50 p-3">
          <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-zinc-800">{data.summary}</p>
          <p className="mt-1 break-words text-xs text-zinc-500">{data.model}</p>
          {data.stale && <div className="mt-2">{action}</div>}
        </div>
      ) : (
        !summary.isPending && <div className="mx-5 my-2">{action}</div>
      )}
    </div>
  )
}
