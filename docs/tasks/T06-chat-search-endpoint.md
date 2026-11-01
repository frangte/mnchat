# T06: Chat search endpoint, timeout, breaker

**Status:** todo · **Size:** M · **Depends on:** T00 · **Blocks:** T07, T13

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

- [ ] With `search` **not running**, `make api` starts normally and chat fully works.
- [ ] With `search` down, the endpoint returns 503 `unavailable` within about a second.
- [ ] After 5 failures the breaker opens, calls return almost instantly, and
      `mnchat_search_breaker_state` shows 2.
- [ ] When `search` returns, the breaker closes without a restart.
- [ ] A hit in a channel the caller can't read is dropped. Test with a fake client.
- [ ] A hit on a deleted or missing message is dropped.
- [ ] Hit order is preserved.
- [ ] A caller with no readable channels gets empty results and `search` isn't called.
- [ ] A non-member gets 404. `q` of 201 characters gets 400.
- [ ] `/api/v1/readyz` doesn't change when `search` is down.

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
- Using `c` (gin.Context) as the context passes request cancellation through. Keep doing that.

## Resume notes

Not started.
