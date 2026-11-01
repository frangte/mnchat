# T01: Metrics package and chat instrumentation

**Status:** in-progress (one box left, see Resume notes) · **Size:** S · **Depends on:** T00 · **Blocks:** T12

## Goal

`api` exposes the metrics in CONTRACTS.md at `GET /metrics`, and adds `GET /api/v1/readyz`.

## Why

Grafana needs real request, WebSocket and message data. Tracing is out of scope.

## Owns

- Create `internal/platform/metrics/`
- Change `internal/httpapi/router.go`, `internal/realtime/hub.go`, `internal/service/chat.go`,
  `internal/cmd/api.go`, `go.mod`

T02 is done, so `Publish` already skips clients outside `Event.Audience`. The drop counter
goes in the `default:` branch of its `select`.

## Steps

1. Add `github.com/prometheus/client_golang`.
2. Create `internal/platform/metrics` with a `NewRegistry()` that includes the Go and
   process collectors, plus `Handler(reg) http.Handler`. `search` will reuse this too.
3. Add a gin middleware recording `mnchat_http_requests_total` and
   `mnchat_http_request_duration_seconds`. Use `c.FullPath()` for `route`; when empty, use `unmatched`.
   Skip `/metrics` and skip the duration histogram for `/api/v1/ws`.
4. Expose `GET /metrics` on the root engine, outside `/api/v1` and auth.
5. Add `GET /api/v1/readyz` using `db.PingContext`. It returns 503 if the primary is down,
   and must not check `search`.
6. In the hub, keep `mnchat_ws_clients` updated in `Subscribe` and `removeLocked`, and
   count drops in `Publish`. Keep metric calls outside anything that can block.
7. Count `mnchat_messages_posted_total{kind}` in `PostMessage`. `kind` is `reply` when
   `threadRootID != nil`.

Replace the hub's `slog.Warn` only if it adds nothing. Keep the existing logging style.

## Acceptance

- [x] `curl localhost:8080/metrics` shows every api metric from CONTRACTS.md, except the
      `mnchat_search_*` ones, which T06 adds. Checked on `:18081`, because `:8080` is the
      shared container still running the old image.
- [x] Posting a message and a reply moves `mnchat_messages_posted_total` for each `kind`.
- [x] `route` labels show `/api/v1/workspaces/:wsID/channels` rather than real IDs.
- [ ] Opening and closing a browser tab moves `mnchat_ws_clients` up and down. Not checked
      in a browser, because Chrome isn't installed here. See Resume notes.
- [x] `/api/v1/readyz` returns 200, and 503 with MySQL stopped. The 200 was checked live.
      The 503 is a unit test, because shared MySQL can't be stopped and `mysqldb.Open`
      blocks startup on an unreachable DB. See `TestReadyz`.
- [x] Unit test for the middleware, and hub tests still pass.

## Validate

```bash
make test
make db && make api
curl -s localhost:8080/metrics | grep '^mnchat_'
curl -si localhost:8080/api/v1/readyz | head -1
```

## Gotchas

- WebSocket handlers hijack the connection, so a middleware running after `c.Next()` sees
  the status only when the socket closes.
- Don't use the global Prometheus registry. Pass one in, so tests stay isolated.

## Resume notes

Code is complete. **One check is left:** open the app in a browser against this build, then
close the tab, and watch `mnchat_ws_clients` go up and down. Tick the box and set the status
to `done`. Node 22 WebSocket clients on `:18081` moved the gauge 0, 2, 1, 0, through a clean
close and through `kill -9` with no close frame, so the server side is covered. Only the
browser itself is untested.

**Validate differed from the task text.** `make db` and `make api` weren't run, because MySQL
is shared in this round. The API ran with `HTTP_ADDR=:18081 go run ./cmd/mnchat api --migrate=false`.

**For later tasks:**

- `internal/cmd/api.go` creates `reg`, so T06 can register its `mnchat_search_client_*`
  collectors on it. `search` uses `metrics.NewRegistry()` and `metrics.Handler(reg)`.
- `NewHub`, `NewChatService` and `NewRouter` take new arguments. Hub and chat metrics are
  interfaces declared by their consumers. The collectors are in `internal/platform/metrics/api.go`.

**For T12 dashboards:**

- A WebSocket request reaches `mnchat_http_requests_total` only when the socket closes, with
  status `101`, so open sockets aren't in that counter. Use `mnchat_ws_clients` for those.
- A failed upgrade, such as a bad token, is counted straight away with its real status.
- `method` is `OTHER` for anything outside the standard methods, because `net/http` accepts
  any token and a client could otherwise create unlimited series.
- `mnchat_http_*` series appear after their first request. `mnchat_messages_posted_total`
  exports both kinds from 0.

**Why the code is ordered this way:**

- `requestMetrics` sits outside `gin.Recovery()`. Inside it, a panic skips the metric.
- `MessagePosted` runs right after `Create`, because `PostMessage` can return early with a
  stored message when the member lookup fails.
- The hub's `slog.Warn` stays, because it carries the `user_id` the counter doesn't.

No change to CONTRACTS.md was needed.
