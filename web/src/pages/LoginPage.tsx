import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api'
import { useAuth } from '../auth'
import { Avatar } from '../components/Avatar'
import { Spinner } from '../components/Spinner'

export function LoginPage() {
  const { login } = useAuth()
  const users = useQuery({ queryKey: ['demo-users'], queryFn: api.demoUsers })
  const [pendingId, setPendingId] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)

  const signIn = async (id: number) => {
    setPendingId(id)
    setError(null)
    try {
      await login(id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign-in failed')
      setPendingId(null)
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center bg-gradient-to-br from-indigo-50 via-white to-zinc-100 px-4 py-12">
      <div className="w-full max-w-md">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-4 flex size-12 items-center justify-center rounded-xl bg-indigo-600 text-xl font-bold text-white shadow-lg shadow-indigo-600/30">
            M
          </div>
          <h1 className="text-2xl font-semibold tracking-tight">Sign in to MNChat</h1>
          <p className="mt-2 text-sm text-zinc-500">
            Demo mode: pick a user, no password needed.
            <br />
            Open a second tab as someone else to watch messages arrive live.
          </p>
        </div>

        <div className="rounded-2xl border border-zinc-200 bg-white p-2 shadow-sm">
          {users.isPending && (
            <div className="flex justify-center py-10">
              <Spinner />
            </div>
          )}

          {users.isError && (
            <div className="px-4 py-8 text-center text-sm">
              <p className="font-medium text-zinc-800">Can't reach the API</p>
              <p className="mt-1 text-zinc-500">
                Is it running? Try <code className="rounded bg-zinc-100 px-1">make db api</code>
              </p>
              <button
                onClick={() => users.refetch()}
                className="mt-4 rounded-lg border border-zinc-300 px-3 py-1.5 font-medium hover:bg-zinc-50"
              >
                Retry
              </button>
            </div>
          )}

          {users.data?.map((u) => (
            <button
              key={u.id}
              onClick={() => signIn(u.id)}
              disabled={pendingId !== null}
              className="flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition hover:bg-zinc-50 focus-visible:outline-2 focus-visible:outline-indigo-500 disabled:cursor-wait"
            >
              <Avatar id={u.id} name={u.display_name} />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{u.display_name}</span>
                <span className="block truncate text-sm text-zinc-500">{u.email}</span>
              </span>
              {pendingId === u.id ? (
                <Spinner className="size-4" />
              ) : (
                <span aria-hidden className="text-zinc-300">
                  →
                </span>
              )}
            </button>
          ))}
        </div>

        {error && <p className="mt-3 text-center text-sm text-red-600">{error}</p>}
      </div>
    </div>
  )
}
