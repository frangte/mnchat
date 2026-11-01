# T04: search-service with text search

**Status:** done · **Size:** M · **Depends on:** T00, T03 · **Blocks:** T05, T10, T12, T13

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
   - Split on anything that isn't a letter, digit or underscore. See Resume notes.
   - Drop tokens under 2 characters and default stopwords, keep at most 8, and build `+token*`.
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

- [x] `grpcurl -plaintext localhost:9190 list` or a small test client reaches the service.
- [x] Finding text: `go`, `deploy` (matching `deployed`), and two-word queries needing both words.
- [x] Queries containing stopwords still work, for example `how to deploy`. See Gotchas.
- [x] Workspace 1 results never include workspace 2 rows, tested with shared words.
- [x] A channel not in `channel_ids` never appears, tested with `#leadership`.
- [x] Empty `channel_ids` returns nothing.
- [x] Characters like `+-><()~*"@` in queries can't break or change the query.
- [x] With the replica stopped, the process stays up and returns `UNAVAILABLE`. Simulated, because
      the replica is shared: a closed port, plus a proxy that cuts connections. See Resume notes.
- [x] Unit tests for the query builder. Integration tests behind `//go:build integration`.
- [x] `make test` passes, including `internal/archtest`.

## Validate

```bash
make test
go test -tags integration ./internal/search/...   # needs `make db` and `make api` once
make search
curl -s localhost:9191/metrics | grep mnchat_search
```

## Gotchas

- **A required stopword empties the whole query.** Tested on real MySQL, see
  [VERIFIED-FACTS.md](VERIFIED-FACTS.md). The query builder drops the default list.
- `MATCH` columns must match the index exactly, so use `body` alone.
- Interface placement: declare `Store` in `server`, the consumer, as `service/chat.go` does.
- `search` must start with the replica down, so the chaos test can restart things in any order.

## Resume notes

Done. Every Acceptance item and Validate step was run on 2026-11-01: `make search` on 9190 and
9191, `grpcurl` for each acceptance query, `docker-compose config --quiet`, and the image on
19190 and 19191 against the shared replica.

**Differences from the steps above:**

- **Underscores stay inside tokens.** Step 3 said to split on anything that isn't a letter or
  digit. The index stores `snake_case` as one token, so `+snake* +case*` found nothing. `_` is
  not a boolean operator, so this is safe, and the integration test covers it.
- **A word over 84 characters returns no hits.** MySQL silently ignores a longer term, so
  `deploy <85 chars>` would otherwise return every `deploy` row. No indexed word can be that
  long, so the correct result is empty.
- **The 8-term cap applies after dropping stopwords and duplicates**, so a long question still
  keeps its real words.
- `grpc.reflection` is registered as well, which is what makes `grpcurl list` work.

**The replica wasn't really stopped,** because it's shared. A closed port and a black-holed IP
covered startup and requests on the real binary. `TestRecoversWhenReplicaReturns` puts a proxy in
front of the replica and cuts live pooled connections, then restores them. T13 should confirm a
real `docker stop` of the replica.

**Behaviour for T06, T12 and T13:**

- A closed port returns `UNAVAILABLE` in about 15 ms. A black-holed replica returns
  `DEADLINE_EXCEEDED` at search's own 2 s limit, or at chat's 800 ms deadline first. Both are
  in the breaker's failure list, and chat maps both to 503.
- `/healthz` and `grpc.health.v1` report that the process is up, and stay healthy during a replica
  outage on purpose. Nothing here is a replica readiness signal. Use replication metrics for that.
- A cancelled caller is recorded as `Canceled`, not as a replica failure.
- `SearchThreads` and `SummarizeThread` are in the metrics from zero.
- A query with no searchable words, such as `how to`, returns empty hits without touching
  the database, so it succeeds during an outage.

**For T05:**

- `SearchThreads` and `SummarizeThread` come from the embedded `Unimplemented` type. Add the
  methods in new files under `server/`, widen the `Store` interface, and add arguments to `New`.
- Reuse `clampLimit(limit, 5, 20)` and `storeError`. Each RPC needs its own timeout, because
  the 2 s one is for `SearchMessages`.
- `Metrics` reads RPC names from the generated service description, so no change is needed
  for `mnchat_search_grpc_*`. Put the `mnchat_ai_*` collectors in `internal/search/ai`.
- `config.Config` already loads `AI_PROVIDER` and the `OLLAMA_*` variables, and nothing uses them yet.
- `GRPC.Shutdown` waits 8 s, then cancels, so a `SummarizeThread` that runs longer is cut off.

**Known limits, all accepted for this demo:**

- `go` matches `good` and `goes`, because every term is a prefix. That's the behaviour step 3
  asks for, and `deploy` needs it to match `deployed`.
- `how to` alone returns nothing, since no word in it is searchable.
- `case` doesn't find `snake_case`, because that's one token. Only a prefix of it matches.
- Accents and CJK don't match well: `cafe` missed `café` and `日本語` missed itself. They run
  without errors, and non-English search is out of scope.
- `channel_ids` has no size limit of its own. MySQL allows 65,535 placeholders, so tested on the
  real binary, 65,531 IDs work and 65,532 return `UNAVAILABLE` (error 1390). Chat is the only
  caller, and no workspace comes close.

**Running the integration tests:** they need `make db` and `make api` once. Rows are inserted
through the primary as `t04-it ...`, deleted by ID, and the test waits for the deletion to
replicate. The store tests own those rows, and the server tests read seed data only, because
`go test` runs packages at the same time. Overrides: `SEARCH_MYSQL_DSN`, `SEARCH_TEST_PRIMARY_DSN`,
`SEARCH_TEST_ROOT_DSN` and `SEARCH_TEST_REPLICA_ADDR`.

`CONTRACTS.md` gained a note on the `rpc` and `code` label values, and otherwise nothing changed.
