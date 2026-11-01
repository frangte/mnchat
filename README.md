### MNChat

Building a SAAS service, that handle chat for org like slack (multitenant, org chat)


Features:
- Create org, workspace like slack 
- Create channels, invite people 
- Role managements
- Support context search (Embedding run at end of day then), raw search message, AI summary chat for long threads
- UI with react.

Techstack:
- Golang for building web API
- Mysql (latest)
- Setup deploy local on minikube (helm chart setup also)

Design: [docs/design.md](docs/design.md)

## Run locally

```bash
make db    # MySQL 9 primary and read-only replica in docker (docker-compose)
make api   # Go API on :8080 — applies migrations + demo seed on startup
make web   # React dev server on http://localhost:5173
```

The replica needs a clean start, so if you ran the project before replication was added,
run `make reset-db` once. `make replica-status` shows replication health.

Demo login: pick any seeded user (no password). Open a second tab as another user to see
messages arrive live. Carol belongs to both orgs (Acme, Globex) and can switch workspaces;
users in one org can't see the other's data.

`make test` runs `go vet` and unit tests · `make reset-db` wipes data (re-seeded on next start).
