# MNChat — Design (demo scope)

A small Slack-like, multi-tenant chat app. Built as an interview demo: every
choice favours "simple and working" and the scale-up path is noted instead of built.

## Architecture

```
React SPA (Vite) ──HTTP + WebSocket──► mnchat api (Go, single binary)
                                          ├── MySQL 9   data, FULLTEXT, VECTOR
                                          ├── /data     uploaded files (PVC)
                                          ├── Claude    thread summaries
                                          └── Voyage    embeddings
mnchat worker (k8s CronJob, nightly) ──► embeds new/edited messages
```

- One Go binary: `mnchat api | worker | migrate`.
- REST and WebSocket are served by the same process; an in-memory hub fans out
  events. *Scale-up:* Redis pub/sub + separate ws gateway.
- If `ANTHROPIC_API_KEY` / `VOYAGE_API_KEY` are unset, fake providers are used
  so the demo runs offline.

## Tenancy & roles

`Org → Workspace → Channel → Message (→ thread replies)`. Shared schema; every
query is scoped by `workspace_id` taken from the URL after a membership check.

| Scope     | Roles                    |
|-----------|--------------------------|
| Org       | owner, admin, member     |
| Workspace | admin, member            |
| Channel   | member (public / private / dm) |

Rules:
- Org owner/admin act as admin in every workspace of the org.
- Org owner/admin: create workspaces, manage org members.
- Workspace admin: settings, remove members, delete any message, archive channels.
- Any workspace member: invite people, create channels, join public channels.
- `canRead(channel)` = public **or** caller is a channel member. Used by messages,
  files, search and summaries. Admins do not read private channels they're not in.
- Private resources the caller can't see return **404**.

## Data model

```sql
users            (id, email UQ, password_hash, display_name, created_at)
orgs             (id, name, created_by, created_at)
org_members      (org_id, user_id, role)                         PK(org_id, user_id)
workspaces       (id, org_id, name, created_at)
workspace_members(workspace_id, user_id, role, joined_at)        PK(workspace_id, user_id)
invites          (id, workspace_id, token UQ, role, created_by, expires_at)
channels         (id, workspace_id, type ENUM('public','private','dm'),
                  name NULL, dm_key NULL, created_by, archived_at)
                  UQ(workspace_id, name), UQ(workspace_id, dm_key)
channel_members  (channel_id, user_id, last_read_msg_id)         PK(channel_id, user_id)
messages         (id AUTO_INCREMENT, workspace_id, channel_id, thread_root_id NULL,
                  user_id, body TEXT, reply_count, created_at, edited_at, deleted_at)
                  INDEX(channel_id, thread_root_id, id), FULLTEXT(body)
attachments      (id, workspace_id, message_id NULL, uploader_id, path, filename, mime, size)
message_chunks   (id, workspace_id, channel_id, first_msg_id, last_msg_id,
                  content TEXT, embedding VECTOR(512), created_at)
embedding_state  (workspace_id PK, last_msg_id, last_run_at)
thread_summaries (thread_root_id PK, last_msg_id, summary TEXT, created_at)
```

- DMs are channels with `type='dm'`; `dm_key` = sorted member ids (`"3:9"`).
- Invites are shareable links (no email server).

## API (`/api/v1`, JSON, `Authorization: Bearer <jwt>`)

`{ws}` = `/workspaces/{wsID}`

| Area      | Endpoints |
|-----------|-----------|
| Auth      | `POST /auth/signup` · `POST /auth/login` · `GET /me` |
| Orgs      | `POST /orgs` · `GET /orgs` · `GET /orgs/{id}/members` · `PATCH /orgs/{id}/members/{uid}` |
| Workspaces| `POST /orgs/{id}/workspaces` · `GET /orgs/{id}/workspaces` · `GET {ws}/members` · `PATCH/DELETE {ws}/members/{uid}` |
| Invites   | `POST {ws}/invites` → link · `POST /invites/{token}/accept` |
| Channels  | `GET {ws}/channels` · `POST {ws}/channels` · `POST {ws}/channels/{ch}/join` · `POST {ws}/channels/{ch}/members` · `POST {ws}/dms {user_ids}` |
| Messages  | `GET {ws}/channels/{ch}/messages?before=&limit=` · `POST {ws}/channels/{ch}/messages {body, thread_root_id?, attachment_ids?}` · `PATCH/DELETE {ws}/messages/{id}` · `GET {ws}/messages/{id}/replies` |
| Files     | `POST {ws}/files` (multipart, ≤10MB) · `GET {ws}/files/{id}` (after `canRead`) |
| Search    | `GET {ws}/search?q=&mode=text\|semantic` |
| AI        | `POST {ws}/messages/{id}/summary` (cached by last reply id) |
| Admin     | `POST {ws}/embeddings/run` — trigger the nightly job now (for the demo) |
| Realtime  | `GET /ws?token=<jwt>` |

Errors: `{"error": {"code": "not_found", "message": "..."}}`.

WebSocket events (server → client): `message.created | message.updated |
message.deleted | channel.created | member.added`, shape
`{"type", "channel_id", "data"}`. On reconnect the client refetches the open channel.

## Search

- **Text:** `MATCH(body) AGAINST(? IN NATURAL LANGUAGE MODE)` limited to readable channels.
- **Semantic:** nightly job groups messages into chunks (a thread, or ~10
  consecutive channel messages), embeds them with Voyage (512 dims), stores them in
  `message_chunks.embedding`. A query is embedded, candidate chunks from readable
  channels are loaded, and cosine similarity is computed in Go (top 10).
  Behind a `VectorIndex` interface. *Scale-up:* Qdrant / HeatWave.
- Edited/deleted messages cause their chunk to be re-embedded on the next run.

## AI thread summary

Threads with ≥ 5 replies show "Summarize". The thread is sent to
`claude-haiku-5-5`; result cached in `thread_summaries` until a new reply arrives.

## Stack

- **Go:** gin, database/sql + go-sql-driver/mysql, goose migrations, golang-jwt,
  bcrypt, coder/websocket, slog.
- **Web:** React + Vite + TypeScript, TanStack Query, Tailwind.
- **Local:** `docker compose up` (mysql + api + web) for fast iteration.
- **Minikube:** one Helm chart `deploy/helm/mnchat` — api Deployment, web
  Deployment (nginx), MySQL StatefulSet, migrate Job (hook), worker CronJob,
  PVC for files, Ingress at `mnchat.local`, Secret for API keys.

## Build order

1. Scaffold: Go module, migrations, docker-compose, Vite app, Helm skeleton.
2. Auth + orgs + workspaces + invites + roles.
3. Channels, DMs, messages, threads, WebSocket, chat UI.
4. File uploads.
5. Text search, AI summary.
6. Embedding worker + semantic search.
7. Helm on minikube end-to-end, README demo script.
