# T07: Chat summary endpoints, thread results, seed threads

**Status:** done · **Size:** M · **Depends on:** T03, T06 · **Works end to end with:** T05

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
   - `Upsert(ctx, s, embedding []float32)`, binding the embedding as raw bytes: 4,096 bytes,
     each float32 little-endian. Not `STRING_TO_VECTOR`, which rejects subnormal floats with
     error 6138. Reject anything but 1024 finite values before writing.
3. Add `MessageRepo.LatestReplyID(ctx, workspaceID, rootID)`.
4. `service/summary.go`, shared access check, in this order:
   1. Member check, then load the root with `messages.Get`.
   2. `channels.GetVisible` on the root's channel. Failures return `ErrNotFound`.
   3. The root must be top-level, otherwise `ErrInvalid`.
5. `GET`: return the summary with `Stale` set from `LatestReplyID`, or `nil` when there's none
   yet, which the handler sends as `200` with a `null` body.
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

- [x] Clicking through the flow stores one row, and a second `POST` doesn't call `search`.
      *Live: second `POST` in 5 ms, search's `SummarizeThread` counter unchanged.*
- [x] After a new reply, `GET` shows `stale: true`, and `POST` regenerates. *(fakes)*
- [x] A summary is still returned with `search` down, if it's cached. *(fakes, and over HTTP
      with a closed search port)*
- [x] With `search` down and no cached summary, `POST` returns 503, and chat is unaffected.
      *(over HTTP with a closed search port: 503 `unavailable`, nothing stored)*
- [x] A thread with 2 replies returns 400. A reply as `:msgID` returns 400. *(fakes, over HTTP)*
- [x] A caller who can't read the channel gets 404 on `GET` and `POST`, and no `search` call.
      *(fakes, over HTTP; the hidden thread's summary isn't even read)*
- [x] `mode=threads` finds a thread by meaning, for example `why did the website go down`
      finding the deploy thread. *Live: 5 of 5 topic queries ranked the right thread first.*
- [x] Carol never gets the `#leadership` thread, even with a query matching it closely.
      *Live: 5 queries as Carol, including the restructuring one, never returned it.*
- [x] The row appears on the replica, and `embedding` has 1024 dims. *Live: 6 rows on the
      replica, `VECTOR_DIM` 1024 each.*
- [x] Service tests with fakes cover all of these branches.

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
- `GET` returns `200` with a `null` body for "no summary yet", so the browser console stays
  clean in the demo. The owner chose this on 2026-11-01, replacing the earlier 404. A 404 now
  only means the root is missing or hidden, and `ThreadSummary.tsx` shows it as an error.

## Resume notes

**2026-11-01, built in a worktree with fakes. Status stays `in-progress` until the end-to-end
checks below pass in the main tree with T05 merged.**

Done:
- `domain.ThreadSummary` and `domain.EmbeddingDims` (1024). The IDs are `json:"-"`, so a
  response has exactly `summary, last_msg_id, model, stale, updated_at`.
- `repo/summary.go`: `Get` (`ErrNotFound` if none), `GetMany`, and `Upsert` with
  `INSERT ... AS new ON DUPLICATE KEY UPDATE`. Reads never select the embedding. `Upsert`
  rejects anything but 1024 finite floats before writing, and never replaces a summary with
  one covering fewer replies, since two POSTs can finish out of order.
- The embedding is bound as raw bytes (4,096, float32 little-endian), not through
  `STRING_TO_VECTOR`. T05 found that `STRING_TO_VECTOR` rejects subnormal floats with error
  6138, and I confirmed it on 9.7.4. `TestSummaryUpsertInATransaction`
  (`-tags integration`) runs the real `Upsert`, `Get` and `GetMany` on the primary in one
  rolled-back transaction: subnormals, -0 and MaxFloat32 come back bit for bit, an older
  summary doesn't replace a newer one, and no rows are left.
- `MessageRepo.LatestReplyID` and the batched `LatestReplyIDs`, one grouped query that joins
  through the root so it uses `idx_messages_channel` (checked with `EXPLAIN`). Deleted
  replies don't count.
- `service/summary.go`: access check is member, root, `GetVisible` on the root's channel (any
  failure is `ErrNotFound`, and unexpected errors are logged), then top-level (`ErrInvalid`).
  POST: under 3 replies is 400, a summary covering the newest reply returns without `search`,
  otherwise `SummarizeThread`, `Upsert`, read the row back, then compute `Stale`. A response
  with an empty summary or a bad embedding is `ErrUnavailable` and stores nothing.
- `SearchService.Threads` and `mode=threads`: limit 5 by default, 20 max, capped hits, roots from
  the primary, only readable top-level roots reach the summary and reply queries, threads
  without a summary dropped, search's order kept, repeats dropped. `q` validation is shared
  with messages mode.
- Routes `GET` and `POST /workspaces/:wsID/messages/:msgID/summary`, wired in `cmd/api.go`.
- Migration `00006`: deploy and rollback, late Northwind payment, October cloud bill overrun
  (all `#general`), team lunch (`#random`), Support restructuring (`#leadership`, Alice and
  Bob only). Up and Down were dry-run on the shared primary in one rolled-back transaction:
  reply counts match, every poster is a channel member, Carol can't read the `#leadership`
  thread, and Down removes everything, including a summary through the foreign key.

**Contract change:** `GET .../summary` with no summary yet is now `200` with a `null` body,
not `404`. CONTRACTS.md, `web/src/api.ts` (`ThreadSummary | null`) and `ThreadSummary.tsx`
(a 404 is now an error, not "no summary") changed with it. `T09-summary-ui.md` still
describes the old 404 behavior, and `README.md` still needs the decision recorded.

**2026-11-01, main tree, after merging T05: done.** All the end-to-end checks below passed
against Compose `api` and `search`, real Ollama and the replica. VERIFIED-FACTS.md, "Round 3
end to end", has the numbers. `TestLatestReplyIDs` ran too, as part of
`go test -tags integration ./internal/repo/...`, and cleaned up after itself. The one store test
that broke, `TestFindsText`, assumed no seeded row says "rollback"; `00006` has one, so its
single-word cases now use the fixture-only word "kestrel".

The original checklist, kept for reference:
- `make db`, `make search`, `make api` (applies `00006`), then click through: POST once,
  POST again with no `search` call, add a reply, GET shows `stale: true`, POST regenerates.
- Stop `search`: a cached GET and POST still work, and an uncached POST returns 503.
- `mode=threads&q=why did the website go down` finds the deploy thread once it's summarized.
- As Carol, a query matching the restructuring thread never returns it.
- On the replica: `SELECT thread_root_id, VECTOR_DIM(embedding) FROM thread_summaries` shows 1024.
- `MNCHAT_TEST_MYSQL_DSN=... go test ./internal/repo/ -run TestLatestReplyIDs` checks the
  latest-reply query against a real DB. It commits and then deletes messages, like
  `TestGetManyFilters`, so not during a demo. It wasn't run here. The query's SQL was checked
  with `EXPLAIN` and in a rolled-back transaction.
- Read every seeded summary that `gemma3:4b` writes before the interview.
