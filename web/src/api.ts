export type User = { id: number; email: string; display_name: string }

export type Workspace = {
  id: number
  org_id: number
  org_name: string
  name: string
  role: 'admin' | 'member'
}

export type Channel = {
  id: number
  workspace_id: number
  type: 'public' | 'private' | 'dm'
  name: string
  is_member: boolean
  created_at: string
}

export type Message = {
  id: number
  workspace_id: number
  channel_id: number
  thread_root_id: number | null
  user_id: number
  user_name: string
  body: string
  reply_count: number
  created_at: string
}

export type ThreadSummary = {
  summary: string
  last_msg_id: number
  model: string
  stale: boolean
  updated_at: string
}

export type ServerEvent =
  | { type: 'message.created'; channel_id: number; data: Message }
  | { type: 'channel.created'; channel_id: number; data: Channel }

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// sessionStorage (not localStorage) so each browser tab can be a different demo user.
const TOKEN_KEY = 'mnchat.token'

export function getToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export function setToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(TOKEN_KEY, token)
    else sessionStorage.removeItem(TOKEN_KEY)
  } catch {
    // storage unavailable (private mode): session lasts until reload
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(`/api/v1${path}`, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    const payload = await res.json().catch(() => null)
    throw new ApiError(
      res.status,
      payload?.error?.code ?? 'error',
      payload?.error?.message ?? res.statusText,
    )
  }
  return res.json() as Promise<T>
}

type List<T> = { items: T[] }

export const api = {
  demoUsers: () => request<List<User>>('GET', '/auth/demo-users').then((r) => r.items),
  demoLogin: (userId: number) =>
    request<{ token: string; user: User }>('POST', '/auth/demo-login', { user_id: userId }),
  me: () => request<User>('GET', '/me'),
  workspaces: () => request<List<Workspace>>('GET', '/workspaces').then((r) => r.items),
  channels: (wsId: number) =>
    request<List<Channel>>('GET', `/workspaces/${wsId}/channels`).then((r) => r.items),
  createChannel: (wsId: number, name: string) =>
    request<Channel>('POST', `/workspaces/${wsId}/channels`, { name }),
  messages: (wsId: number, chId: number) =>
    request<List<Message>>('GET', `/workspaces/${wsId}/channels/${chId}/messages?limit=100`).then(
      (r) => r.items,
    ),
  replies: (wsId: number, rootId: number) =>
    request<List<Message>>('GET', `/workspaces/${wsId}/messages/${rootId}/replies?limit=100`).then(
      (r) => r.items,
    ),
  summary: (wsId: number, rootId: number) =>
    request<ThreadSummary>('GET', `/workspaces/${wsId}/messages/${rootId}/summary`),
  summarize: (wsId: number, rootId: number) =>
    request<ThreadSummary>('POST', `/workspaces/${wsId}/messages/${rootId}/summary`),
  postMessage: (wsId: number, chId: number, body: string, threadRootId?: number) =>
    request<Message>('POST', `/workspaces/${wsId}/channels/${chId}/messages`, {
      body,
      thread_root_id: threadRootId,
    }),
}

export function socketURL(workspaceId: number, token: string) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${proto}://${location.host}/api/v1/ws?workspace_id=${workspaceId}&token=${encodeURIComponent(token)}`
}
