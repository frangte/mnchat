# T07: Chat summary endpoints, thread results, seed threads

**Status:** todo · **Size:** M · **Depends on:** T03, T06 · **Works end to end with:** T05

## Goal

Add `GET` and `POST /workspaces/:wsID/messages/:msgID/summary`, finish `mode=threads` on the
search endpoint, and seed long English threads for the demo.

## Why

`api` stays the only database writer, so the replica can remain truly read-only. The summary
reaches the replica through normal replication, which is what makes the thread searchable.

## Owns

- Create `internal/repo/summary.go`, `internal/service/summary.go`, `internal/service/summary_test.go`
- Create `internal/infra/mysqldb/migrations/00006_demo_threads.sql`
- Change `internal/service/search.go`, `internal/httpapi/handlers.go`, `internal/httpapi/router.go`,
  `internal/cmd/api.go`, `internal/domain/domain.go`, `internal/repo/message.go`

## Steps

1. `domain.ThreadSummary`: fields from CONTRACTS.md, plus `Stale bool`.
2. `repo/summary.go`:
   - `Get(ctx, workspaceID, rootID)`
   - `GetMany(ctx, workspaceID, rootIDs)`
   - `Upsert(ctx, s, embedding []float32)`, writing with `STRING_TO_VECTOR(?)` and a
     JSON-style `[0.1,0.2]` string.
3. Add `MessageRepo.LatestReplyID(ctx, workspaceID, rootID)`.
4. `service/summary.go`, shared access check, in this order:
   1. Member check, then load the root with `messages.Get`.
   2. `channels.GetVisible` on the root's channel. Failures return `ErrNotFound`.
   3. The root must be top-level, otherwise `ErrInvalid`.
5. `GET`: return the summary with `Stale` set from `LatestReplyID`, or `ErrNotFound`.
6. `POST`:
   1. `reply_count < 3` returns `ErrInvalid` with a clear message.
   2. If a stored summary covers `LatestReplyID`, return it without calling `search`.
   3. Otherwise call `SummarizeThread`, upsert, and return with `Stale` computed.
7. Add a `summary` breaker in `searchclient`, if T06 didn't include one, with the 60 s deadline.
8. `SearchService.Threads`: readable channels, `SearchThreads`, load roots from the primary,
   load summaries, drop anything unreadable or without a summary, keep order, set `Stale`.
9. Migration `00006`: 4 or 5 threads in English, each with at least 5 replies, on clearly
   different topics: a failed deploy and rollback, a late vendor payment, a budget overrun,
   a team lunch. Put one in `#leadership` to prove semantic permissions. Use `SET @root`
   like `00003`, and set `reply_count`.

## Acceptance

- [ ] Clicking through the flow stores one row, and a second `POST` doesn't call `search`.
- [ ] After a new reply, `GET` shows `stale: true`, and `POST` regenerates.
- [ ] A summary is still returned with `search` down, if it's cached.
- [ ] With `search` down and no cached summary, `POST` returns 503, and chat is unaffected.
- [ ] A thread with 2 replies returns 400. A reply as `:msgID` returns 400.
- [ ] A caller who can't read the channel gets 404 on `GET` and `POST`, and no `search` call.
- [ ] `mode=threads` finds a thread by meaning, for example `why did the website go down`
      finding the deploy thread.
- [ ] Carol never gets the `#leadership` thread, even with a query matching it closely.
- [ ] The row appears on the replica, and `embedding` has 1024 dims.
- [ ] Service tests with fakes cover all of these branches.

## Validate

```bash
make test
ollama serve
make db && make search & make api
# POST a summary, then:
docker-compose exec -T mysql-replica mysql -uroot -proot mnchat \
  -e "SELECT thread_root_id, VECTOR_DIM(embedding), LEFT(summary,60) FROM thread_summaries"
```

## Gotchas

- `last_msg_id` from `search` can be older than the primary's newest reply if the replica
  lags. Compute `Stale` after the upsert and let the UI offer "Update summary".
- `gemma3:4b` can get details wrong. Read every seeded summary before the interview.
- Summaries are empty on a fresh DB, so the demo must click [Summary] first.
- Don't hold a DB transaction across the `SummarizeThread` call. It can take seconds.
- `GET` returning 404 for "no summary yet" is the contract, and T09 treats it as a normal state.
  Browsers still log it, once per thread opened. If that looks bad in the demo, changing it to
  `200` with a null body is a small change in T07 and `ThreadSummary.tsx`.

## Resume notes

Not started.
