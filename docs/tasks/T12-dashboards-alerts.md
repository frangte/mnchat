# T12: Grafana dashboards and alert rules

**Status:** in-progress · **Size:** M · **Depends on:** T01, T04, T05, T10 · **Blocks:** T14

## Goal

Two provisioned dashboards and a set of Prometheus alert rules that tell the failure story
on screen.

## Why

The strongest demo moment is killing search and watching chat stay flat while replica lag
and breaker state react.

## Owns

- Create `deploy/observability/grafana/dashboards/chat-overview.json`
- Create `deploy/observability/grafana/dashboards/search-pipeline.json`
- Create `deploy/observability/prometheus/rules/mnchat.yml`
- Change `deploy/observability/prometheus/prometheus.yml` only to load the rules

## Dashboards

**MNChat Overview**, the one to show first:

| Panel | Query basis |
|---|---|
| Chat request rate | `mnchat_http_requests_total` |
| Chat error rate | Same, with `status=~"5.."`, excluding the search route |
| Chat p95 latency | `mnchat_http_request_duration_seconds` |
| WebSocket clients | `mnchat_ws_clients` |
| Messages per minute | `mnchat_messages_posted_total` by `kind` |
| Service status | `up` for each job |

**Search Pipeline:**

| Panel | Query basis |
|---|---|
| Replica lag | Metric T10 recorded in CONTRACTS.md |
| Replication running | Same |
| Search p95 and rate | `mnchat_search_grpc_*` |
| Breaker state | `mnchat_search_breaker_state`, mapped to Closed, Half-open, Open |
| Client outcomes | `mnchat_search_client_requests_total` by `outcome` |
| AI latency | `mnchat_ai_duration_seconds` by `op` |
| AI tokens | `mnchat_ai_tokens_total` |

The search route must be excluded from the chat error panel. Otherwise a search outage looks
like a chat failure, which is the opposite of what the demo shows.

## Alert rules

| Alert | Condition | For |
|---|---|---|
| `ChatHighErrorRate` | Non-search 5xx over 1% | 2m |
| `ChatHighLatency` | Non-search p95 over 300 ms | 5m |
| `SearchServiceDown` | `up{job="search"} == 0` | 1m |
| `SearchBreakerOpen` | Breaker state is 2 | 1m |
| `ReplicaLagHigh` | Lag over 30 s | 2m |
| `ReplicationStopped` | Replication threads not running | 1m |

Alerts stay visible in Prometheus and Grafana. Alertmanager is out of scope.

## Acceptance

- [x] Both dashboards load with no manual import, and every panel has data under traffic.
      *Provisioned into the MNChat folder, Overview is Grafana's home. Under `make load` plus one
      summary, all 15 panel queries returned data, except Firing alerts, which shows "None" when
      nothing fires.*
- [x] Killing `search`: chat panels stay flat, `up` drops, the breaker opens, the alert fires.
      *Checked through Prometheus during a 3 min outage under load: chat 5xx share 0 on every
      sample, chat p95 at most 65 ms, `SearchBreakerOpen` fired after 68 s and `SearchServiceDown`
      after 73 s.*
- [x] Killing the replica: lag or running state changes, and chat panels stay flat.
      *`mysql_up{role="replica"}` goes to 0, the thread series vanish, `ReplicationStopped` fired
      after 74 s, chat flat.*
- [x] Dashboard JSON is checked in with no hard-coded datasource UID that breaks on a fresh start.
      *Every panel uses the provisioned fixed `uid: prometheus`. Grafana keeps no state, and a
      recreated container loaded both dashboards.*
- [x] `promtool check rules` passes. *6 rules, and `promtool check config` too.*
- [ ] Screenshots of both dashboards look clean at a projector-friendly zoom. *Not done: no
      browser was available to the session. The owner checks it.*

## Validate

```bash
make up
docker-compose exec -T prometheus promtool check rules /etc/prometheus/rules/mnchat.yml
```

Use `make load` from T13 for traffic if it's available.

## Gotchas

- Provision the datasource with a fixed `uid`, and reference it from the dashboards.
- Histogram quantiles need `rate()` over a window longer than the 5 s scrape. Use `[1m]`.
- `SearchBreakerOpen` can't use a plain `state == 2` with `for: 1m`. During a long outage the
  breaker goes half-open every 10 s, and a scrape reading 1 resets the `for` timer, so the alert
  may never fire. Use `max_over_time(mnchat_search_breaker_state[1m]) == 2`, and prove it fires
  during a real outage longer than a few minutes.
- WebSocket requests reach `mnchat_http_requests_total` only when the socket closes, with status
  101. Exclude `/api/v1/ws` from error and rate panels.
- `mnchat_http_*` series appear only after their first request, so panels can be empty on a fresh boot.
- Keep the panel count low. An interviewer looks at each one for a few seconds.

## Resume notes

**2026-11-01, main tree.** Done apart from the screenshot check, which is the owner's.

- **Chat routes** in every chat panel and alert: `route!~"/api/v1/ws|.*/search|.*/summary"`.
  Summary routes are left out as well as search, since `POST .../summary` returns 503 with
  search down. This is recorded in CONTRACTS.md.
- **Overview, 8 panels:** Services (`up` for api and search, `mysql_up` per role), Search
  breakers, Firing alerts (from `ALERTS`), chat requests, chat errors (`or vector(0)`, so it reads
  0 rather than empty), chat p95, messages per minute, WebSocket clients.
- **Search Pipeline, 7 panels:** replica lag, Replication (`or vector(0)` so a dead replica
  reads DOWN, not empty), breaker state timeline, chat's calls to search by outcome, search p95
  and rate on two axes, AI p95 and AI tokens (both over `[5m]`, since AI calls are sparse).
- **`SearchBreakerOpen`** uses `max_over_time(...[1m]) == 2`, as the gotcha says, plus a clause
  needing recent search calls. Without it, the alert would stay firing after search recovers
  with no traffic, because a breaker only leaves open on a call. It resolved within about a
  minute of recovery.
- **`ReplicationStopped`** also fires on `mysql_up{role="replica"} == 0` and on a dead replica
  exporter, because a dead replica exports no thread series.
- **Alert timing:** each alert needs about a minute of outage, so a default 60 s
  `make chaos-search` shows them only as pending. The README gives the longer timeline.
- **Generated JSON:** the dashboards were generated by a short script, but the JSON is the source
  of truth now. Edit it directly, keep `"uid": "prometheus"` on every datasource, and Grafana
  picks changes up within 10 s.
- **Found during validation:** `search`'s embed warm-up stalled for its whole 30 s three times
  during the first chaos runs, while every later restart warmed in about 55 ms. The cause isn't
  confirmed. `cmd/mnchat-search/main.go` now retries the warm-up every 10 s for 5 minutes, which
  also covers Ollama starting after `search`.
