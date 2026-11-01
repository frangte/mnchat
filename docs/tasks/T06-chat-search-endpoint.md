# T06: Chat search endpoint, timeout, breaker

**Status:** done, proven against a fake `search` only (see Resume notes) · **Size:** M · **Depends on:** T00 · **Blocks:** T07, T13

## Goal

`GET /api/v1/workspaces/:wsID/search` with `mode=messages`, calling `search` over gRPC in a
way that can't hurt chat. T07 adds `mode=threads`.

## Why

This is where "search dies, chat is fine" gets enforced: lazy connection, deadline and breaker.
It also keeps permissions in chat, so `search` needs no copy of them.

## Owns

- Create `internal/searchclient/`, `internal/service/search.go`, `internal/service/search_test.go`
- Change `internal/httpapi/handlers.go`, `internal/httpapi/router.go`, `internal/cmd/api.go`,
  `internal/config/config.go`, `internal/repo/message.go`, `go.mod`

## Steps

1. `internal/searchclient`: use `grpc.NewClient`, which connects lazily, with insecure
   credentials. Keep it out of the startup path, and don't call `WaitForReady`.
2. Wrap each RPC with its own deadline from CONTRACTS.md and its own `gobreaker` breaker.
   Only `UNAVAILABLE`, `DEADLINE_EXCEEDED` and `RESOURCE_EXHAUSTED` count as failures.
3. Map gRPC codes to domain errors as in CONTRACTS.md. Record `mnchat_search_client_*` metrics.
4. `internal/service/search.go`: declare a `SearchClient` interface here, the consumer, and add
   a `SearchService` with its own constructor. Leave `NewChatService` unchanged.
5. `SearchService.Messages`:
   1. `RequireMember` style check, via `WorkspaceStore`. A non-member gets `ErrNotFound`.
   2. Trim `q`, and reject empty or over 200 characters with `ErrInvalid`.
   3. `ListVisible` to get channel IDs. No channels means empty results, with no call to `search`.
   4. Call `SearchMessages`.
   5. Load messages from the primary with a new `MessageRepo.GetMany(ctx, workspaceID, ids)`.
   6. Drop any message whose channel isn't in the readable set.
   7. Keep the hit order.
6. Add the handler and route. Validate `mode`: `messages`, or `threads` which returns `ErrInvalid`
   until T07. Unknown values also return `ErrInvalid`.
7. Wire it in `api.go`. Add `SearchGRPCAddr` to config.

## Acceptance

- [x] With `search` **not running**, `make api` starts normally and chat fully works. Run as
      the built API on `:18084` with `--migrate=false`: channels, post, reply, list and a
      WebSocket event all worked, including with the breaker open.
- [x] With `search` down, the endpoint returns 503 `unavailable` within about a second.
      Live: refused 6–14 ms, hanging server 807–837 ms, silent TCP server 830 ms at worst,
      black-holed IP 845 ms at worst.
- [x] After 5 failures the breaker opens, calls return almost instantly, and
      `mnchat_search_breaker_state` shows 2. Live, in all four failure modes.
- [x] When `search` returns, the breaker closes without a restart. Live against the fake
      `search`, through the real 10 s window, and the gauge returned to 0.
- [x] A hit in a channel the caller can't read is dropped. Tested with a fake client and a
      malicious gRPC server, and live on MySQL with private message 9.
- [x] A hit on a deleted or missing message is dropped. Live on MySQL, and in `TestGetManyFilters`.
- [x] Hit order is preserved. Live: fake hits `2,9,5,4,999999,103,1` returned `2,4,1`.
- [x] A caller with no readable channels gets empty results and `search` isn't called.
      Unit and router tests only: every seeded user can read a public channel.
- [x] A non-member gets 404. `q` of 201 characters gets 400. Live.
- [x] `/api/v1/readyz` doesn't change when `search` is down. Live, including with the breaker
      open and during a hang, when it answered in 3–10 ms.

## Validate

```bash
make test
make api &    # search not running
TOKEN=$(curl -s -XPOST localhost:8080/api/v1/auth/demo-login -H 'Content-Type: application/json' \
  -d '{"user_id":1}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
curl -si -H "Authorization: Bearer $TOKEN" 'localhost:8080/api/v1/workspaces/1/search?q=release' | head -1
```

## Gotchas

- `search` returns IDs only. Never trust it for permissions. Step 6 is the second check.
- `gobreaker` v2 is generic, so use `CircuitBreaker[*searchv1.SearchMessagesResponse]`.
- The breaker's failure check needs the gRPC code, so use `IsSuccessful` rather than the
  default of treating every error as a failure.
- `c` (gin.Context) does **not** pass cancellation through: `Done()` returns nil unless
  `ContextWithFallback` is set, and it isn't. Pass `c.Request.Context()` to anything that
  should stop when the client leaves. `TestClosedTabCancelsSearchAndLeavesBreakerAlone` covers it.

## Resume notes

Code is complete. **Nothing here has run against the real `search` yet**, because T04 isn't
merged. All of it was proven with a fake: a real `grpc.Server`, so the client, deadline and
breaker are real, but the responses are not.

**Validate differed from the task text.** MySQL and `:8080` are shared, so the API ran from a
built binary with `HTTP_ADDR=:18084 --migrate=false`, and the fake ran on `19290`.

**After T04 merges, check:**

- A real search end to end. That also covers the `INVALID_ARGUMENT` and `NOT_FOUND` paths
  from a real server, which only the fake has produced so far.
- Stopping `search` in Compose. `passthrough` was chosen from reading gRPC's `dns` resolver,
  which backs off up to 120 s after a lookup fails, and Compose drops a stopped container's
  name. That lookup failure hasn't been reproduced here, so it is reasoned, not measured.

**For later tasks:**

- **T10:** set `SEARCH_GRPC_ADDR=search:9190` on the `api` service. This task doesn't own
  `docker-compose.yml`, and without it `api` looks for search on its own `localhost`.
- **T07:** `searchclient.Client` already has `SearchThreads` and `SummarizeThread`, with their
  deadlines and breakers, so don't add a `summary` breaker. Add them to the `SearchClient`
  interface in `service/search.go` and add a `threads` case to `searchWorkspace`, which
  rejects it for now. Thread limits differ from messages: default 5, max 20.
- **T12:** `route` is `/api/v1/workspaces/:wsID/search`, so exclude that from chat error
  panels. The three breaker series exist from startup. The request counter's series appear on
  first use. A closed tab counts as `outcome="error"`, and shows as a 503 in
  `mnchat_http_requests_total`, though no client reads it.
- **T13:** a breaker only leaves open on a call, so the gauge stays 2 until the next search
  after `search` returns. Keep polling search, or recovery looks stuck.

**Why the code is this way:**

- **`IsExcluded`, not `IsSuccessful`, for a caller going away.** As a success, a closed tab
  would reset the failure count, or close a half-open breaker while search was still dead.
  Excluded calls also free the half-open slot, so a cancelled probe can't wedge it. Checked in
  the source of gobreaker v2.4.0, the pinned version, so recheck if it changes.
- **Reconnect backoff capped at 2 s.** Measured after a 90 s outage: 5.0 s to the first 200,
  against 24.9 s with gRPC's default backoff, which grows to 120 s. Those are one run each.
- **A silent server can fail before the deadline.** With `MinConnectTimeout` at 2 s, the third
  call to a server that never speaks ends early with `UNAVAILABLE`. 800 ms is a limit, not a
  fixed time, and the call still counts as a breaker failure.
- **Hits are capped at `limit`** before `GetMany`, so a bad response can't build a huge `IN` list.
- **The gRPC wait holds no MySQL connection**, because `ListVisible` closes its rows first. A
  slow search can't drain the pool chat uses.

**`GetMany` test.** `TestGetManyFilters` needs a database and skips without one. To run it:
`MNCHAT_TEST_MYSQL_DSN='mnchat:mnchat@tcp(127.0.0.1:3306)/mnchat?parseTime=true&loc=UTC' go test ./internal/repo/...`
Its rows start with `t06-` and it deletes them.

**CONTRACTS.md changed:** `RESOURCE_EXHAUSTED` and every other unlisted code now map to
`ErrUnavailable`, and it now states the rules for a caller going away, half-open, the `rpc`
values, the `outcome` meanings and what the histogram covers.
