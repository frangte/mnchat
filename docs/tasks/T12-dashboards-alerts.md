# T12: Grafana dashboards and alert rules

**Status:** todo · **Size:** M · **Depends on:** T01, T04, T05, T10 · **Blocks:** T14

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

- [ ] Both dashboards load with no manual import, and every panel has data under traffic.
- [ ] Killing `search`: chat panels stay flat, `up` drops, the breaker opens, the alert fires.
- [ ] Killing the replica: lag or running state changes, and chat panels stay flat.
- [ ] Dashboard JSON is checked in with no hard-coded datasource UID that breaks on a fresh start.
- [ ] `promtool check rules` passes.
- [ ] Screenshots of both dashboards look clean at a projector-friendly zoom.

## Validate

```bash
make up
docker-compose exec -T prometheus promtool check rules /etc/prometheus/rules/mnchat.yml
```

Use `make load` from T13 for traffic if it's available.

## Gotchas

- Provision the datasource with a fixed `uid`, and reference it from the dashboards.
- Histogram quantiles need `rate()` over a window longer than the 5 s scrape. Use `[1m]`.
- Keep the panel count low. An interviewer looks at each one for a few seconds.

## Resume notes

Not started.
