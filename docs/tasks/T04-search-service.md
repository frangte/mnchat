# T04: search-service with text search

**Status:** todo · **Size:** M · **Depends on:** T00, T03 · **Blocks:** T05, T10, T12, T13

## Goal

A separate binary and image serving `SearchMessages` over gRPC, reading only the replica views.

## Why

This is the microservice in the demo. It has its own process, image, config and database
connection, and shares no code with chat.

## Owns

- Create `cmd/mnchat-search/main.go`, `Dockerfile.search`
- Create `internal/search/config/`, `internal/search/server/`, `internal/search/store/`
- Change `docker-compose.yml` (add the `search` service only), `Makefile` (`make search`)

Use `internal/platform/metrics` from T01. If T01 isn't merged yet, create that package
to the shape T01 describes.

## Steps

1. `internal/search/config`: load the env vars from CONTRACTS.md. Don't import `internal/config`.
2. `internal/search/store`: open the replica connection with its own small `Open`, and don't
   import `internal/infra/mysqldb`. Don't fail startup if the replica is down: log it, serve,
   and return `UNAVAILABLE` per request.
3. Build the boolean query from user text:
   - Split on anything that isn't a letter or digit.
   - Drop tokens under 2 characters, keep at most 8, and build `+token*`.
   - No usable tokens means empty hits, not an error.
4. Query with bound parameters, plus one `?` per channel ID:

   ```sql
   SELECT id, MATCH(body) AGAINST(? IN BOOLEAN MODE) AS score
   FROM search_messages_v1
   WHERE workspace_id = ? AND channel_id IN (...)
     AND MATCH(body) AGAINST(? IN BOOLEAN MODE)
   ORDER BY score DESC, id DESC
   LIMIT ?
   ```

5. `internal/search/server`: implement `SearchMessages`.
   - `workspace_id <= 0` or empty query: `INVALID_ARGUMENT`.
   - Empty `channel_ids`: empty hits. Never all channels.
   - Limit default 20, max 50.
   - DB errors: `UNAVAILABLE`.
   - `SearchThreads` and `SummarizeThread` return `UNIMPLEMENTED` until T05.
6. Register `grpc.health.v1` and a unary interceptor recording the `mnchat_search_grpc_*` metrics.
7. Serve `/metrics` and `/healthz` on `SEARCH_METRICS_ADDR`. Handle SIGTERM with `GracefulStop`.
8. `Dockerfile.search`: mirror `Dockerfile`, copying `cmd`, `gen` and `internal`, and building
   `./cmd/mnchat-search`.
9. Add compose service `search`, ports `9190:9190` and `9191:9191`, with
   `depends_on: mysql-replica: condition: service_healthy` and
   `extra_hosts: ["host.docker.internal:host-gateway"]`.

## Acceptance

- [ ] `grpcurl -plaintext localhost:9190 list` or a small test client reaches the service.
- [ ] Finding text: `go`, `deploy` (matching `deployed`), and two-word queries needing both words.
- [ ] Queries containing stopwords still work, for example `how to deploy`. See Gotchas.
- [ ] Workspace 1 results never include workspace 2 rows, tested with shared words.
- [ ] A channel not in `channel_ids` never appears, tested with `#leadership`.
- [ ] Empty `channel_ids` returns nothing.
- [ ] Characters like `+-><()~*"@` in queries can't break or change the query.
- [ ] With the replica stopped, the process stays up and returns `UNAVAILABLE`.
- [ ] Unit tests for the query builder. Integration tests behind `//go:build integration`.
- [ ] `make test` passes, including `internal/archtest`.

## Validate

```bash
make test
go test -tags integration ./internal/search/...   # needs `make db` and `make api` once
make search
curl -s localhost:9191/metrics | grep mnchat_search
```

## Gotchas

- **Stopwords are untested.** The default InnoDB list includes words like `how` and `to`. A
  required stopword may return nothing. If so, drop default stopwords in the query builder.
- `MATCH` columns must match the index exactly, so use `body` alone.
- Interface placement: declare `Store` in `server`, the consumer, as `service/chat.go` does.
- `search` must start with the replica down, so the chaos test can restart things in any order.

## Resume notes

Not started.
