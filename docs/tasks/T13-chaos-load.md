# T13: k6 load test and `make chaos-search`

**Status:** todo · **Size:** M · **Depends on:** T04, T06, T10 · **Blocks:** T14

## Goal

An automated test that kills search and the replica under chat traffic and **fails if chat
returns any error**.

## Why

"Search dies, chat isn't affected" is the owner's core requirement. This turns it from a
claim into a repeatable result that can run live in the interview.

## Owns

- Create `deploy/load/chat.js`, `scripts/chaos-search.sh`
- Change `Makefile` (`load`, `chaos-search`), `docker-compose.yml` (a `k6` service under a `load` profile)

## Steps

1. `deploy/load/chat.js`, k6:
   - Log in through `/api/v1/auth/demo-login`.
   - Each virtual user loops: post a message, list messages, sometimes post a reply.
   - Tag every request `group: chat`. Search requests, in a separate scenario, get `group: search`.
   - Thresholds on `group:chat`: `http_req_failed` rate is 0, and p95 under 300 ms.
   - `make load` runs 5 virtual users for 60 s by default.
2. `scripts/chaos-search.sh`, using `set -euo pipefail`:
   1. Check the stack is healthy.
   2. Record a unique marker message with the API.
   3. Start k6 in the background.
   4. After 15 s, `docker-compose stop search`.
   5. After 15 s more, `docker kill` the replica as well.
   6. While both are down, post a second unique marker.
   7. Check the search endpoint returns 503 quickly.
   8. Start both again.
   9. Wait for k6 to finish, and capture its exit code.
   10. Poll search until the second marker is found, up to 60 s.
   11. Print a clear pass or fail summary.
3. `make chaos-search` runs the script. A non-zero exit means failure.

## Acceptance

- [ ] `make chaos-search` passes with zero chat errors across the outage.
- [ ] The marker sent during the outage is searchable after recovery.
- [ ] A deliberately broken setup fails the test. Prove it by making `api` depend on `search`
      temporarily, then revert.
- [ ] The script restores `search` and the replica even if it fails partway, using `trap`.
- [ ] Grafana shows the outage and recovery clearly during the run.
- [ ] It's safe to run repeatedly, and test data stays identifiable by a prefix.

## Validate

```bash
make up
make chaos-search; echo "exit=$?"
```

## Gotchas

- Run k6 inside Compose so it reaches `api` by service name, using `api:8080`.
- The demo users all post, so `reply_count` and threads will grow. Use the `load-test` prefix.
- k6 thresholds decide its exit code, so that's the pass or fail signal for chat.
- `docker kill` needs the real container name, such as `mnchat-mysql-replica-1`. Check it
  with `docker-compose ps`, because the name depends on the project folder.
- The search scenario is expected to fail during the outage. Exclude it from chat thresholds.

## Resume notes

Not started.
