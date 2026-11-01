# MNChat task board

Work for the ANDPAD Vietnam senior interview demo. Every task is self-contained, so
it can run in its own session and be resumed with little context.

## Resume in 30 seconds

Paste this into a new session:

> Read `docs/tasks/README.md`, `docs/tasks/CONTRACTS.md` and `docs/tasks/T03-mysql-replication.md`, then do that task.

Rules for whoever picks up a task:

1. Read this file, then [CONTRACTS.md](CONTRACTS.md), then the task file.
2. Check the task's **Depends on** list against the board below.
3. Set the task to `in-progress` here and in the task file.
4. Stay inside the task's **Owns** list. If a contract has to change, change
   CONTRACTS.md in the same task and say so in the task's Resume notes.
5. Before stopping, even mid-task, fill in **Resume notes** and update the board.
6. Done means every **Acceptance** box is ticked and **Validate** was actually run.
7. Commit only when the owner asks.

## Board

Status values: `todo`, `in-progress`, `blocked`, `done`.

| ID | Task | Size | Status | Depends on |
|---|---|---|---|---|
| [T00](T00-contracts.md) | Contracts, proto, shared error, import boundary test | S | done | none |
| [T01](T01-metrics.md) | Metrics package and chat instrumentation | S | in-progress | T00 |
| [T02](T02-private-ws-leak.md) | Fix private-channel WebSocket leak, seed private channel | S | done | none |
| [T03](T03-mysql-replication.md) | MySQL replication, views, read-only user | M | done | none |
| [T04](T04-search-service.md) | search-service with text search | M | todo | T00, T03 |
| [T05](T05-summarize-semantic.md) | Thread summaries and semantic search with Ollama | M | todo | T04 |
| [T06](T06-chat-search-endpoint.md) | Chat search endpoint, timeout, breaker | M | todo | T00 |
| [T07](T07-chat-summary-endpoint.md) | Chat summary endpoints, thread results, seed threads | M | todo | T03, T06 |
| [T08](T08-search-ui.md) | Search input and results panel | S | in-progress | contract only |
| [T09](T09-summary-ui.md) | Summary button and card in the thread panel | S | in-progress | contract only |
| [T10](T10-observability-stack.md) | Prometheus, Grafana, exporters, `make up` | S | todo | T03, T04 |
| [T11](T11-mock-interview.md) | Mock interview questions | S | in-progress | none |
| [T12](T12-dashboards-alerts.md) | Grafana dashboards and alert rules | M | todo | T01, T04, T05, T10 |
| [T13](T13-chaos-load.md) | k6 load test and `make chaos-search` | M | todo | T04, T06, T10 |
| [T14](T14-docs-demo-script.md) | Update docs, write ADRs and the demo script | S | todo | everything |

## Suggested order

```
T00, T02, T03 (done)
 ├─ T01
 ├─ T04 ─ T05 ─┐
 ├─ T06 ─ T07 ─┼─ T10 ─ T12, T13 ─ T14
 ├─ T08, T09 ──┘
 └─ T11 (any time)
```

**Worktree rounds.** Each round runs in parallel, one worktree per task, branched from
`feat/search-observability` and merged back serially.

| Round | Tasks | Why together |
|---|---|---|
| 1 | T01, T08, T09, T11 | Go, web and docs. T08 and T09 only overlap in `web/src/api.ts` |
| 2 | T04, T06 | Both need T01's metrics package, so they wait for it |
| 3 | T05, T07 | T05 needs T04, T07 needs T06 |
| 4 | T10 | Needs T04 in Compose |
| 5 | T12, T13 | Need the running stack |
| 6 | T14 | Needs everything |

In a worktree, don't edit this board, stop or reset shared containers, or use ports 8080 or 5173.

**Shared hot files:** `internal/cmd/api.go`, `internal/httpapi/router.go`, `go.mod`,
`docker-compose.yml`, `Makefile`, `web/src/pages/ChatPage.tsx`, `web/src/api.ts`.
Run `go mod tidy` after merging any task that adds a dependency.

## Scope decisions

These came from the owner on 2026-11-01. Don't reopen them without asking.

| Decision | Detail |
|---|---|
| Search store | MySQL replica fed by native replication. **No Elasticsearch.** |
| Monitoring | Prometheus scraping and Grafana. **No tracing, no OpenTelemetry for now.** |
| Semantic search | **Threads only**, via AI summaries the user triggers with a [Summary] button. No per-message embeddings. |
| AI | **Local Ollama:** `gemma3:4b` and `bge-m3`. No Claude or Voyage. |
| Language | **English only for now.** No language detection, no `ngram`, no Japanese or Vietnamese tuning. |
| UI | Keep the current UI. The only additions are the search input and panel and the Summary button and card. |
| Local data | `make reset-db` may wipe the local DB, which holds seed data only. The owner approved this. |
| Deploy | Compose only. Helm and minikube are out of scope. |
| Mock interview | Questions an ANDPAD interviewer would ask, with answer notes based on this project. |

## Environment

- macOS on Apple M1 Pro, 32 GB RAM. Docker runs in Colima with 8 GB and no GPU.
- Use `docker-compose`, not `docker compose`. The plugin form isn't installed.
- Ollama runs natively on the Mac. Don't put it in Compose.
- `protoc`, `buf`, `protoc-gen-go` and `protoc-gen-go-grpc` are installed.
- Colima mounts only `$HOME`, so mounts from `/tmp` or the scratchpad show up empty.
- The shell is zsh with `noclobber`, so `>` won't overwrite a file. Use `>|`, or a bash script.
- Don't batch a destructive command with edits. If the hook blocks an edit, the command still runs.
- Disk is tight, with about 12 GB free. Check before pulling more images or models.
- The GateGuard hook asks for facts before the first edit of each file. Answer it and retry.

See [VERIFIED-FACTS.md](VERIFIED-FACTS.md) for what was tested and what failed.
