# T01: Metrics package and chat instrumentation

**Status:** todo · **Size:** S · **Depends on:** T00 · **Blocks:** T12

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

- [ ] `curl localhost:8080/metrics` shows every api metric from CONTRACTS.md, except the
      `mnchat_search_*` ones, which T06 adds.
- [ ] Posting a message and a reply moves `mnchat_messages_posted_total` for each `kind`.
- [ ] `route` labels show `/api/v1/workspaces/:wsID/channels` rather than real IDs.
- [ ] Opening and closing a browser tab moves `mnchat_ws_clients` up and down.
- [ ] `/api/v1/readyz` returns 200, and 503 with MySQL stopped.
- [ ] Unit test for the middleware, and hub tests still pass.

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

Not started.
