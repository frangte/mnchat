# Verified facts

Results from throwaway experiments on 2026-11-01, using `mysql:9` (9.7.4 Community)
and Ollama 0.35.0 on the owner's M1 Pro. Trust these over assumptions. Containers were
removed afterwards, so nothing here exists in the repo yet.

## MySQL replication

| Check | Result |
|---|---|
| Fresh replica built only from `docker-entrypoint-initdb.d` scripts | Works, using GTID and `SOURCE_AUTO_POSITION=1` |
| `mnchat` database and users `mnchat`, `repl`, `search_reader` reach the replica | Yes, they replicate from the primary. Don't set `MYSQL_DATABASE` or `MYSQL_USER` on the replica |
| Replica auto-starts after `CHANGE REPLICATION SOURCE TO` in init, no `START REPLICA` | Yes |
| `SET PERSIST super_read_only = ON` in the replica init script | Works and survives restart |
| 500 inserts on primary with replica up | 1.0–1.3 ms per insert |
| 500 inserts on primary with replica killed | 0.9 ms per insert, so **no slowdown** |
| Replica returns after missing 1,000 rows | Caught up automatically with no manual step |
| Index present during the insert timings | An `ngram` `FULLTEXT` index on `body`, so the cost of maintaining an index is included. The real schema uses the default parser, which wasn't timed |
| Semi-sync plugins | Not loaded by default. Keep it that way, because semi-sync would couple chat to the replica |
| `binlog_expire_logs_seconds` default | 2592000 (30 days) |

Later confirmed in the real repo with T03: 50 chat posts over HTTP with the replica killed
had zero errors, and it caught up with no manual step.

### Full stack outage test

Run on 2026-11-01 in Compose with real `api`, `search`, primary and replica. This is one laptop run
with a small data set, and `make chaos-search` (T13) replaces it.

| Event | Result |
|---|---|
| `docker-compose stop search`, then 30 chat posts | Zero errors, `readyz` stayed 200 |
| Search calls while stopped | 503. First two about 810 ms, then 399 ms, then 2–5 ms once the breaker opened |
| `docker-compose start search` | First 200 after 8.8 s, breaker closed, no chat restart |
| `docker-compose stop mysql-replica`, then 30 chat posts | Zero errors, `readyz` stayed 200 |
| Search calls while replica stopped | 503 at about 810 ms each, then 9 ms once the breaker opened |
| `docker-compose start mysql-replica` | A message sent during the outage was searchable 9.6 s later |

Permissions held end to end: Alice found both `reorganization` messages, and Carol and Dave found none.

## Views and grants

| Check | Result |
|---|---|
| `MATCH ... AGAINST` through a `MERGE` view with `WHERE deleted_at IS NULL` | Works |
| View with a `JOIN` to `users` | Readable by `search_reader` on the replica |
| `search_reader` reading base tables | Denied with error 1142, as wanted |
| View created by the limited `mnchat` user, then used on the replica | Works. The definer exists there |
| `GRANT` on the views from a migration | Works if `mnchat` has `GRANT OPTION` on `mnchat.*` |

**Startup crash to avoid:** `GRANT SELECT ON mnchat.some_view` inside an init script
fails with error 1146, because the view doesn't exist until migrations run, and the
primary container then exits with code 1. Init scripts create users only. Grants on
views belong in the migration, right after `CREATE VIEW`.

## Migrations

Goose refuses to start if a lower-numbered migration appears after a higher one was applied:
`found 1 missing migrations before current version 5`. This happened with `00004` after `00005`.

## English text search

Tested with `--innodb-ft-min-token-size=2` on both servers and the default parser.

| Query, `IN BOOLEAN MODE` | Matches |
|---|---|
| `+go*` | "we deploy Go services" |
| `+deploy*` | "deploy" and "deployment", so prefix matching works |
| `+release* +planning*` | Only the row with both words |
| `+rollout* +release*` | Nothing, since no row has both |

- With the default minimum of 3, `Go` finds nothing. That setting must be on **both**
  servers **before** the index is built, so it needs a fresh database.

### Stopwords, underscores and long words

T04 tested these on the real replica as `search_reader`, with the default parser and
`innodb_ft_enable_stopword=1`. The test row is `how to deploy the gateway`.

| Query, `IN BOOLEAN MODE` | Matches |
|---|---|
| `+how* +to* +deploy*` | **Nothing**, although the row has all three words |
| `+how +to +deploy` | Nothing |
| `+how* +deploy*`, `+to* +deploy*` | Nothing |
| `+deploy*` | The row, plus `deployed` |
| `+how*` | Only `however`, never `how` |
| `+to*` | Only `today` and `tomorrow`, never `to` |
| `+the*`, `+in*`, `+how`, `+to` | Nothing |

- **A required stopword empties the whole query**, because stopwords are never indexed. The
  `*` doesn't help: it matches longer words that start the same way. `search` drops these words in
  the query builder, so `how to deploy` runs as `+deploy*`.
- The list has 36 rows and 35 distinct words, because `the` appears twice. The Go copy is in
  `internal/search/store/query.go`, and `TestStopwordListMatchesServer` compares it with
  `information_schema.INNODB_FT_DEFAULT_STOPWORD`. That table needs `PROCESS`, so the test reads
  it as root.
- A query of only stopwords, such as `how to`, returns no hits.

| Token | Result |
|---|---|
| `snake_case` in a body | One index token. `+snake_case*` and `+snake*` match, while `+case*` and `+snake* +case*` don't |
| `_private_`, `__dunder__` in a body | Also one token each. `+private*` and `+dunder*` don't match |
| `don't`, `o'brien` in a body | The apostrophe splits: `+don*` and `+brien*` match |
| `well-known`, `www.example.com` | Split at `-` and `.`. `+well* +known*` and `+example*` match |
| `10:00` | `+10* +00*` matches |
| `2fa`, `v2`, `2026` | Each matches with `+token*`, including the all-digit one |
| A term of 84 characters | Treated as a real term, so a query requiring it finds nothing |
| A term of 85 or more characters | **Silently ignored:** `+deploy* +<85 chars>*` returns the `+deploy*` rows, with no error |
| `café` in a body | `+café*` matches, `+cafe*` doesn't |
| `日本語` in a body | `+日本語*` doesn't match. Runs without error. Non-English search is out of scope |

**Injection.** Each of `+-><()~*"@`, unbalanced quotes and brackets, `'`, `\`, `%`, a NUL byte and
`'; DROP TABLE messages; --` ran through `SearchMessages` on real MySQL with no error and no change
to the required terms. For example `kestrel -deploy` still requires `deploy`.

**Query plan.** Prepared with bound `?` in both `AGAINST` clauses, as the Go driver sends it, the
plan is `Full-text index search on messages using ft_messages_body`, then a filter on
`workspace_id`, `channel_id` and `deleted_at`. The data set was only seed rows, so this says nothing
about speed at volume.

**Scores** in a small table can be identical across rows, so `id DESC` is what makes the order stable.

## Vectors

| Check | Result |
|---|---|
| `VECTOR` column replicates | Yes |
| `DISTANCE()` and `VECTOR_DISTANCE()` | **Don't exist in Community.** Compute cosine in Go |
| Stored format | 4 bytes per dimension, little-endian float32. `[1,0,0,0]` is `0000803F` then zeros |
| Writing | `STRING_TO_VECTOR('[1,0,0,0]')` works |
| Reading through the Go MySQL driver | **Untested.** T05 must confirm it arrives as `[]byte` |

## Ollama

Ollama must run natively, because Colima containers have no GPU. Metal was detected
and models ran 100% on GPU.

| Model | Size on disk | Result |
|---|---|---|
| `gemma3:4b` | 3.4 GB | **Use for summaries.** About 2 s, about 4 s cold. Accurate on the English test threads |
| `bge-m3` | 1.2 GB | **Use for embeddings.** 1024 dims, about 18 ms per query, 8 of 8 correct |
| `nomic-embed-text` | 274 MB | Also installed. 5 of 8, failing on non-English queries. Not used |
| `qwen3:4b` | 2.5 GB | Rejected and removed. Writes reasoning into the output even with `think:false` and `/no_think` |
| `qwen2.5:3b` | 1.9 GB | Rejected and removed. Faster, but got facts wrong |

- Together the chosen models use about 4 GB of RAM.
- A message saying "ignore all previous instructions" didn't take over a summary.
- Containers reach native Ollama at `host.docker.internal:11434` with
  `extra_hosts: ["host.docker.internal:host-gateway"]`, even though Ollama is bound to
  `127.0.0.1`. So there's no need for `OLLAMA_HOST=0.0.0.0`, which would expose it to the network.
- Matching scores were 0.49–0.66 and the best non-match 0.38–0.54, so a fixed cutoff
  doesn't work. Show the top few results.
- The sample was 4 threads and 8 queries. It shows the approach works, but it isn't a benchmark.

Ollama isn't left running. Start it with `ollama serve` or the Ollama app.
