# T10: Prometheus, Grafana, exporters, `make up`

**Status:** in-progress · **Size:** S · **Depends on:** T03, T04 · **Blocks:** T12, T13

## Goal

`make up` starts the whole backend and monitoring stack, and Prometheus scrapes everything.

## Why

Monitoring is a demo requirement, and the owner chose scraping with Prometheus and Grafana.
No tracing and no OpenTelemetry.

## Owns

- Create `deploy/observability/prometheus/prometheus.yml`
- Create `deploy/observability/grafana/provisioning/datasources/prometheus.yml`
- Create `deploy/observability/grafana/provisioning/dashboards/dashboards.yml`
- Change `docker-compose.yml` (monitoring services only), `Makefile`

## Steps

1. Pin exact image versions for `prom/prometheus`, `grafana/grafana` and
   `prom/mysqld-exporter`. Never use `latest`.
2. Add `mysqld-exporter` and `mysqld-exporter-replica`, using the `exporter` user from T03,
   no published ports. Enable the replica-status collector on the replica one.
3. Add `prometheus`: mount the config and a `prometheus-data` volume, port `9090:9090`,
   and mount `deploy/observability/prometheus/rules/` for T12.
4. Scrape every 5 s for a lively demo:

   ```yaml
   - job_name: api
     static_configs: [{ targets: ['api:8080'] }]
   - job_name: search
     static_configs: [{ targets: ['search:9191'] }]
   - job_name: mysql
     static_configs:
       - targets: ['mysqld-exporter:9104']
         labels: { role: primary }
       - targets: ['mysqld-exporter-replica:9104']
         labels: { role: replica }
   ```

5. Add `grafana`, port `3000:3000`, anonymous access with the `Viewer` role, and provisioned
   datasource and dashboard folder (`deploy/observability/grafana/dashboards/`).
6. `Makefile`: `up` runs `docker-compose up -d --build`, and `down` stops. Note that `make web`
   stays separate.
7. **Record in CONTRACTS.md** the exact exporter metric for replica lag, and for whether
   replication threads are running.

## Acceptance

- [ ] `make up` from a clean state brings everything up without manual steps. *The monitoring
      containers and an empty Prometheus volume came up with no manual step. The full wipe,
      `make reset-db && make up`, was refused by the session's permission classifier, so the
      owner runs it.*
- [x] Prometheus `/targets` shows all 4 targets `UP`. *api, search, mysql primary, mysql replica.*
- [x] The replica lag metric exists and its name is written in CONTRACTS.md.
- [x] Grafana opens at `localhost:3000` with no login and has the datasource. *Home 200 with no
      redirect, and an anonymous query through the datasource returned `count(up==1)` = 4.*
- [x] Stopping `search` turns only that target `DOWN`, and chat keeps working. *readyz and
      messages 200.*
- [x] Stopping Prometheus or Grafana doesn't affect chat. *readyz and messages 200.*
- [x] Total RAM is under about 2.5 GB, checked with `docker stats --no-stream`. *About 1.2 GB
      for the MNChat containers: both MySQL servers about 0.98 GB, Grafana 113 MB, Prometheus
      42 MB, the rest under 11 MB each.*

## Validate

```bash
make up
curl -s localhost:9090/api/v1/targets | python3 -c \
  'import sys,json;[print(t["labels"]["job"],t["health"]) for t in json.load(sys.stdin)["data"]["activeTargets"]]'
docker stats --no-stream
```

## Gotchas

- `api` already has `SEARCH_GRPC_ADDR: search:9190` in Compose, and `search` is already a service.
- `.dockerignore` excludes `deploy/`, so mount those configs as volumes rather than `COPY` them.
- Colima has 8 GB. Ollama doesn't count, since it runs on the Mac.
- `api`'s `/metrics` needs T01. Until that's merged, the `api` target is `DOWN`, which is expected.
- `mysqld-exporter` wants credentials through a config file or `MYSQLD_EXPORTER_PASSWORD`,
  depending on version. Check the docs for the version you pin.
- Older exporters use `slave` in metric names, and some newer ones use `replica`. Don't assume.

## Resume notes

**2026-11-01, main tree.** Done apart from the clean-state box, which needs the owner to run
`make reset-db && make up`.

- **Images:** `prom/prometheus:v3.15.0`, `grafana/grafana:12.4.13`, `prom/mysqld-exporter:v0.20.0`,
  each released at least two weeks earlier. Grafana stays on 12.x, whose classic dashboard JSON
  and provisioning T12 relies on.
- **Exporters:** credentials go in through `--mysqld.address`, `--mysqld.username` and
  `MYSQLD_EXPORTER_PASSWORD`, so there's no `.my.cnf` to mount. The replica's exporter adds
  `--collect.slave_status`. Neither has published ports or `depends_on`: they reconnect on each
  scrape.
- **Replication metrics** are in CONTRACTS.md. They vanish when the replica is down, while
  `mysql_up{role="replica"}` drops to 0. Both were seen by stopping the replica.
- **Prometheus** mounts the whole `deploy/observability/prometheus/` directory at
  `/etc/prometheus`, so T12's `rules/` is already inside. `--web.enable-lifecycle` lets T12
  reload rules with `curl -X POST localhost:9090/-/reload`. Retention is 7 days or 1 GB, since
  the disk is tight.
- **Grafana** is stateless, with no volume. The datasource has a fixed `uid: prometheus`, and
  dashboards load from `/etc/grafana/dashboards` into the MNChat folder. An empty
  `provisioning/alerting/` keeps a startup error out of the logs.
- `make up` runs `docker-compose up -d --build` and prints the URLs. `make web` stays separate.
