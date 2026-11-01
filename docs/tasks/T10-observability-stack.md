# T10: Prometheus, Grafana, exporters, `make up`

**Status:** todo · **Size:** S · **Depends on:** T03, T04 · **Blocks:** T12, T13

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

- [ ] `make up` from a clean state brings everything up without manual steps.
- [ ] Prometheus `/targets` shows all 4 targets `UP`.
- [ ] The replica lag metric exists and its name is written in CONTRACTS.md.
- [ ] Grafana opens at `localhost:3000` with no login and has the datasource.
- [ ] Stopping `search` turns only that target `DOWN`, and chat keeps working.
- [ ] Stopping Prometheus or Grafana doesn't affect chat.
- [ ] Total RAM is under about 2.5 GB, checked with `docker stats --no-stream`.

## Validate

```bash
make up
curl -s localhost:9090/api/v1/targets | python3 -c \
  'import sys,json;[print(t["labels"]["job"],t["health"]) for t in json.load(sys.stdin)["data"]["activeTargets"]]'
docker stats --no-stream
```

## Gotchas

- `.dockerignore` excludes `deploy/`, so mount those configs as volumes rather than `COPY` them.
- Colima has 8 GB. Ollama doesn't count, since it runs on the Mac.
- `api`'s `/metrics` needs T01. Until that's merged, the `api` target is `DOWN`, which is expected.
- `mysqld-exporter` wants credentials through a config file or `MYSQLD_EXPORTER_PASSWORD`,
  depending on version. Check the docs for the version you pin.
- Older exporters use `slave` in metric names, and some newer ones use `replica`. Don't assume.

## Resume notes

Not started.
