# Contracts

The shared agreements between tasks. If a task needs to change something here, it
updates this file in the same change.

## Architecture

```
React ──HTTP/WS──► api (chat) ──gRPC + timeout + breaker──► search ──► Ollama (on the Mac)
                     │                                        │ read-only
               MySQL primary ──async replication──► MySQL replica

Prometheus ──scrapes──► api, search, mysqld-exporter × 2 ──► Grafana
```

**Text search:** `api` checks membership, gets the caller's readable channel IDs and
calls `SearchMessages`. `search` runs `FULLTEXT` on the replica and returns message IDs.
`api` loads those messages from the primary, keeping only readable channels.

**Summary:**
1. `api` checks access and the `thread_summaries` cache on the primary. A fresh summary
   returns immediately, even with `search` down.
2. `api` calls `SummarizeThread`. `search` reads the thread from the replica, then calls
   `gemma3:4b` and `bge-m3`.
3. `api` upserts the result into `thread_summaries` on the primary. `api` is the only writer.
4. Replication copies the row, and the thread becomes searchable by meaning.

**Semantic search:** `api` calls `SearchThreads` with the readable channel IDs. `search`
embeds the query, loads vectors for those channels from the replica, and ranks by cosine in Go.

## Rules

- **Chat never depends on search to run.** No startup dependency, and `/api/v1/readyz`
  checks only the primary. The gRPC client connects lazily.
- **Replication stays asynchronous.** Never enable semi-sync.
- **`search` reads only through the `search_*_v1` views.** Never base tables.
- **`channel_ids` is required.** An empty list returns no hits and never means "all".
- **Every `search` query filters by `workspace_id`.**
- **`internal/search` can't import chat packages, and chat can't import `internal/search`.**
  `internal/archtest` enforces this. Both may import `gen/search/v1` and `internal/platform/*`.
- **Hidden resources return 404**, matching the existing API.
- **Telemetry never blocks a request.**

## Repo layout

| Path | Purpose | Owner |
|---|---|---|
| `proto/search/v1/search.proto` | gRPC contract | T00 |
| `gen/search/v1/` | Generated code, committed | T00 |
| `internal/archtest/` | Import boundary test | T00 |
| `internal/platform/metrics/` | Shared Prometheus helpers | T01 |
| `deploy/mysql/` | Replication config and init scripts | T03 |
| `cmd/mnchat-search/`, `Dockerfile.search` | search binary and image | T04 |
| `internal/search/{config,server,store}` | search core | T04 |
| `internal/search/{ai,semantic}` | Ollama and semantic ranking | T05 |
| `internal/searchclient/` | gRPC client, timeout, breaker | T06 |
| `internal/service/search.go` | Chat search service | T06 |
| `internal/service/summary.go`, `internal/repo/summary.go` | Summaries | T07 |
| `deploy/observability/` | Prometheus and Grafana | T10, T12 |
| `deploy/load/`, `scripts/` | Load and chaos | T13 |

## Ports and services

Compose service names are also the DNS names.

| Service | Port | Host port | Notes |
|---|---|---|---|
| `mysql` | 3306 | 3306 | Primary |
| `mysql-replica` | 3306 | 3307 | Read-only |
| `api` | 8080 | 8080 | REST, WebSocket, `/metrics` |
| `search` | 9190 gRPC, 9191 metrics | 9190, 9191 | Host ports let `make api` reach it |
| `mysqld-exporter` | 9104 | none | Primary |
| `mysqld-exporter-replica` | 9104 | none | Replica |
| `prometheus` | 9090 | 9090 | |
| `grafana` | 3000 | 3000 | Anonymous viewer |
| Ollama | 11434 | native | `host.docker.internal:11434` from containers |
| Vite | 5173 | native | `make web`, proxies `/api` to 8080 |

## Environment variables

| Variable | Used by | Default | Compose value |
|---|---|---|---|
| `SEARCH_GRPC_ADDR` | api | `localhost:9190` | `search:9190` |
| `SEARCH_GRPC_LISTEN` | search | `:9190` | same |
| `SEARCH_METRICS_ADDR` | search | `:9191` | same |
| `SEARCH_MYSQL_DSN` | search | `search_reader:search_reader@tcp(127.0.0.1:3307)/mnchat?parseTime=true&loc=UTC` | host `mysql-replica:3306` |
| `AI_PROVIDER` | search | `ollama` | `ollama`, or `fake` for tests |
| `OLLAMA_URL` | search | `http://localhost:11434` | `http://host.docker.internal:11434` |
| `OLLAMA_CHAT_MODEL` | search | `gemma3:4b` | same |
| `OLLAMA_EMBED_MODEL` | search | `bge-m3` | same |

## MySQL

**Flags on both servers:** `--gtid-mode=ON --enforce-gtid-consistency=ON --innodb-ft-min-token-size=2`.
`--server-id` is 1 on the primary and 2 on the replica.

**Users.** Init scripts create these on the primary, and they replicate:

| User | Password | Purpose |
|---|---|---|
| `repl` | `repl` | `REPLICATION SLAVE` |
| `search_reader` | `search_reader` | `SELECT` on the three views only |
| `exporter` | `exporter` | `PROCESS, REPLICATION CLIENT, SELECT ON *.*` for mysqld-exporter |
| `mnchat` | `mnchat` | App user, given `GRANT OPTION` on `mnchat.*` |

**Migrations:**

| File | Task | Content |
|---|---|---|
| `00004_private_channel.sql` | T02 | Private channel `#leadership` |
| `00005_search_contract.sql` | T03 | `thread_summaries`, views, grants |
| `00006_demo_threads.sql` | T07 | Long English threads |

Goose refuses to start if a lower-numbered migration appears after a higher one was applied,
so new migrations always take the next number. To fix a local mismatch, use `make reset-db`.

```sql
CREATE TABLE thread_summaries (
    thread_root_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    workspace_id   BIGINT UNSIGNED NOT NULL,
    channel_id     BIGINT UNSIGNED NOT NULL,
    last_msg_id    BIGINT UNSIGNED NOT NULL,  -- newest reply the summary covers
    summary        TEXT            NOT NULL,
    model          VARCHAR(100)    NOT NULL,
    embedding      VECTOR(1024)    NOT NULL,
    created_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_thread_summaries_channel (workspace_id, channel_id),
    CONSTRAINT fk_thread_summaries_root FOREIGN KEY (thread_root_id) REFERENCES messages (id) ON DELETE CASCADE
);

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_messages_v1 AS
    SELECT id, workspace_id, channel_id, thread_root_id, body
    FROM messages WHERE deleted_at IS NULL;

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_thread_messages_v1 AS
    SELECT m.id, m.workspace_id, m.channel_id, m.thread_root_id,
           u.display_name AS user_name, m.body, m.created_at
    FROM messages m JOIN users u ON u.id = m.user_id
    WHERE m.deleted_at IS NULL;

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_threads_v1 AS
    SELECT thread_root_id, workspace_id, channel_id, last_msg_id, embedding
    FROM thread_summaries;
```

## gRPC

Defined in `proto/search/v1/search.proto`.

| RPC | Chat deadline | Notes |
|---|---|---|
| `SearchMessages` | 800 ms | Replica only. Works with Ollama down |
| `SearchThreads` | 2 s | Embeds the query. Default limit 5 |
| `SummarizeThread` | 60 s | All-or-nothing: a failed embedding fails the call |

| gRPC code | Meaning | Chat result |
|---|---|---|
| `INVALID_ARGUMENT` | Bad request | `ErrInvalid`, 400 |
| `NOT_FOUND` | Thread not on the replica yet | `ErrNotFound`, 404 |
| `UNAVAILABLE`, `DEADLINE_EXCEEDED` | Replica or Ollama down, or slow | `ErrUnavailable`, 503 |

Only `UNAVAILABLE`, `DEADLINE_EXCEEDED` and `RESOURCE_EXHAUSTED` count as breaker failures.

**Breakers:** one per RPC, using `github.com/sony/gobreaker/v2`. Open after 5 consecutive
failures, stay open 10 s. An open breaker returns `ErrUnavailable` without calling `search`.

## HTTP API

All under `/api/v1`, bearer auth, using the existing error shape
`{"error":{"code":"...","message":"..."}}`.

### `GET /workspaces/:wsID/search?q=&mode=&limit=`

`mode` is `messages` (default) or `threads`. `q` is 1–200 characters after trimming.

```jsonc
// mode=messages, limit default 20, max 50
{ "mode": "messages", "items": [ { "message": { /* Message */ }, "score": 0.42 } ] }

// mode=threads, limit default 5, max 20
{ "mode": "threads", "items": [ { "root": { /* Message */ }, "summary": "...", "stale": false, "score": 0.61 } ] }
```

### `GET /workspaces/:wsID/messages/:msgID/summary`

Returns `200` with the object below, or `404` if there's no summary yet.

### `POST /workspaces/:wsID/messages/:msgID/summary`

Generates or returns a summary. `:msgID` must be a top-level message with at least 3 replies,
otherwise `400`. If the stored summary covers the newest reply, it returns without calling `search`.

```jsonc
{ "summary": "...", "last_msg_id": 42, "model": "gemma3:4b", "stale": false, "updated_at": "..." }
```

`stale` is true when a reply newer than `last_msg_id` exists on the primary. The replica
can lag, so a fresh summary may already be stale. Trust the field, not the timing.

**Errors:** `503` with code `unavailable` when `search`, the replica or Ollama can't serve
the request. `domain.ErrUnavailable` maps to this, and T00 added it.

### `GET /readyz`

Returns `200` when the primary responds. Must not check `search`.

### `GET /metrics`

At the server root, not under `/api/v1`, unauthenticated.

## Metrics

Use exactly these names, so T12 can build dashboards before everything is merged.
`route` is `c.FullPath()`, never the raw URL, to keep cardinality bounded.

**api:**

| Metric | Type | Labels |
|---|---|---|
| `mnchat_http_requests_total` | counter | `method, route, status` |
| `mnchat_http_request_duration_seconds` | histogram | `method, route` |
| `mnchat_ws_clients` | gauge | none |
| `mnchat_ws_dropped_clients_total` | counter | none |
| `mnchat_messages_posted_total` | counter | `kind` = `message`, `reply` |
| `mnchat_search_client_requests_total` | counter | `rpc, outcome` = `ok`, `error`, `unavailable`, `breaker_open` |
| `mnchat_search_client_duration_seconds` | histogram | `rpc` |
| `mnchat_search_breaker_state` | gauge | `rpc`. 0 closed, 1 half-open, 2 open |

Exclude `/api/v1/ws` from `mnchat_http_request_duration_seconds`, because those requests
last as long as the connection.

**search:**

| Metric | Type | Labels |
|---|---|---|
| `mnchat_search_grpc_requests_total` | counter | `rpc, code` |
| `mnchat_search_grpc_duration_seconds` | histogram | `rpc` |
| `mnchat_ai_requests_total` | counter | `op` = `summarize`, `embed`, `outcome` = `ok`, `error` |
| `mnchat_ai_duration_seconds` | histogram | `op` |
| `mnchat_ai_tokens_total` | counter | `direction` = `input`, `output` |

`rpc` is the method name, such as `SearchMessages`. `code` is the Go name of the gRPC code:
`OK`, `InvalidArgument`, `NotFound`, `Unavailable`, `DeadlineExceeded`, `Canceled`, `Unimplemented`.
Health checks aren't counted.

**Replication:** from `mysqld-exporter`. T10 records the exact metric name here, because it
varies by exporter version.

## Web

- Search responses and errors go through `web/src/api.ts`.
- The right-side slot shows **one** panel at a time, thread or search. Search uses the same
  `<aside>` classes as `ThreadPanel`.
- Show `Search is temporarily unavailable. Chat still works.` on a 503.
