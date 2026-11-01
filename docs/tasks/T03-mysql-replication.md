# T03: MySQL replication, views, read-only user

**Status:** done · **Size:** M · **Depends on:** none · **Blocks:** T04, T07, T10

## Goal

`make db` starts a primary and a read-only replica, and migrations create the search
contract: `thread_summaries`, three views, and grants.

## Why

Native replication replaces Elasticsearch and any event pipeline. It's asynchronous, so
losing the replica doesn't affect chat. That's the owner's main requirement.

## Owns

- Create `deploy/mysql/primary-init/01-users.sql`, `deploy/mysql/replica-init/01-replica.sql`
- Create `internal/infra/mysqldb/migrations/00005_search_contract.sql`
- Change `docker-compose.yml` (mysql services only), `Makefile`, `README.md` run section

## Steps

1. `deploy/mysql/primary-init/01-users.sql`:

   ```sql
   CREATE USER 'repl'@'%' IDENTIFIED BY 'repl';
   GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';
   CREATE USER 'search_reader'@'%' IDENTIFIED BY 'search_reader';
   CREATE USER 'exporter'@'%' IDENTIFIED BY 'exporter';
   GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';
   GRANT ALL ON mnchat.* TO 'mnchat'@'%' WITH GRANT OPTION;
   ```

2. `deploy/mysql/replica-init/01-replica.sql`:

   ```sql
   CHANGE REPLICATION SOURCE TO SOURCE_HOST='mysql', SOURCE_USER='repl', SOURCE_PASSWORD='repl',
     SOURCE_AUTO_POSITION=1, GET_SOURCE_PUBLIC_KEY=1, SOURCE_CONNECT_RETRY=2, SOURCE_RETRY_COUNT=100000;
   SET PERSIST super_read_only = ON;
   ```

3. Update `mysql` in `docker-compose.yml`, keeping the existing env and healthcheck:

   ```yaml
   command: --server-id=1 --gtid-mode=ON --enforce-gtid-consistency=ON --innodb-ft-min-token-size=2
   volumes:
     - mysql-data:/var/lib/mysql
     - ./deploy/mysql/primary-init:/docker-entrypoint-initdb.d:ro
   ```

4. Add `mysql-replica`: same image, `--server-id=2` with the other flags, env with
   **only** `MYSQL_ROOT_PASSWORD`, port `3307:3306`, a `mysql-replica-data` volume, the
   replica init mount, the same healthcheck, and `depends_on: mysql: condition: service_healthy`.

5. Add `00005_search_contract.sql` with `thread_summaries` and the three views from
   CONTRACTS.md, then `GRANT SELECT` on each view to `search_reader`. `Down` revokes, then drops.

6. `Makefile`: `make db` starts both services. Add `replica-status`, which runs
   `SHOW REPLICA STATUS` on `mysql-replica`.

7. Update the README run section: the first run after this change needs `make reset-db`.

## Acceptance

- [x] After `make reset-db && make db && make api`, `make replica-status` shows both
      threads `Yes` and no errors.
- [x] All 5 migrations applied on GTID-enabled MySQL, and the replica has every table.
- [x] `search_reader` on port 3307 can read all three views and can't read `messages`.
- [x] `search_reader` can't write, and neither can root on the replica (`super_read_only`).
- [x] A message posted through the API appears in `search_messages_v1` on the replica.
- [x] `FULLTEXT` finds `+go*`, proving `ft_min_token_size=2` took effect.
- [x] `docker kill` the replica, post messages, `docker-compose start mysql-replica`: it
      catches up with no manual step, and chat stayed up.

## Validate

```bash
make reset-db && make db && make api
make replica-status
docker-compose exec -T mysql-replica mysql -h127.0.0.1 -usearch_reader -psearch_reader mnchat \
  -e "SELECT COUNT(*) FROM search_messages_v1"
```

## Gotchas

All of these are verified in VERIFIED-FACTS.md.

- **Never `GRANT` on a view in an init script.** Error 1146 crashes the primary at startup.
- **Init scripts run only on an empty volume.** That's why `make reset-db` is required. The
  owner has approved wiping local data.
- **Don't set `MYSQL_DATABASE` or `MYSQL_USER` on the replica.** They replicate from the primary.
- **`--innodb-ft-min-token-size` goes on both servers.** Each builds its own index.
- **Don't enable semi-sync.**
- `00003` uses `SET @root`. It should run fine under GTID, but acceptance must confirm it.
- Paths must stay under `$HOME` for Colima to mount them, which the repo does.

## Resume notes

Done on 2026-11-01. Verified from a clean `make reset-db`:

- Replication came up from the init scripts alone. 13 tables on both servers.
- `search_reader` holds exactly three `SELECT` grants, and no global privileges.
- With the replica killed, 50 chat posts over HTTP had zero errors. It caught up with no
  manual step and stayed read-only.
- `00005` Down then Up ran cleanly with replication on.
- `make replica-status` shows replication health.

Port 3307 is the replica on the host. `make db` starts both servers, and `api` deliberately
has no dependency on the replica.
