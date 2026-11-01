# T13: k6 load test and `make chaos-search`

**Status:** in-progress · **Size:** M · **Depends on:** T04, T06, T10 · **Blocks:** T14

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

- [x] `make chaos-search` passes with zero chat errors across the outage. *Default run: 0% chat
      failures, p95 22.6 ms. Long run, with search down 3 min 15 s and the replica 85 s: 0%, p95
      23.3 ms.*
- [x] The marker sent during the outage is searchable after recovery. *Found by the time k6 ended,
      in both runs.*
- [x] A deliberately broken setup fails the test. Prove it by making `api` depend on `search`
      temporarily, then revert. *`postMessage` temporarily ran a search first: 32% of chat
      requests failed, the outage post got 503, and the script exited 1. Reverted, and
      `git diff` on `handlers.go` is empty.*
- [x] The script restores `search` and the replica even if it fails partway, using `trap`. *The
      first run died at step 7 and the trap brought both back. It also exposed a leftover k6
      container, so k6 now runs with a per-run name that the trap removes.*
- [ ] Grafana shows the outage and recovery clearly during the run. *The panel data was checked
      through Prometheus during the run, see T12. Nobody has looked at it yet: no browser was
      available to the session.*
- [x] It's safe to run repeatedly, and test data stays identifiable by a prefix. *Run 5 times.
      Everything goes to `#load-test`, with `load-test` bodies and `loadtest…<epoch>` markers.*

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

**2026-11-01, main tree.** Done apart from watching Grafana during a run, which is the owner's.

- `deploy/load/chat.js` uses k6's built-in `scenario` tag rather than a custom `group` tag,
  since k6 reserves `group` for `group()` blocks. Thresholds are on `{scenario:chat}`. Search has
  an always-passing threshold, only so the summary shows its failure rate.
- k6 `setup()` creates `#load-test`, or reuses it on a 409, and logs in the 4 Acme demo users.
  Posting to a public channel joins it, so all of them can post.
- `scripts/chaos-search.sh` follows the 11 steps. Timings come from `SEARCH_DOWN_AT`,
  `REPLICA_DOWN_AT`, `RESTORE_AT` and `LOAD_SECONDS`, defaulting to 15, 30, 45 and 60. It needs
  `jq`, and checks for it first.
- **Two bash pitfalls, both fixed:**
  - `read` returns 1 at end of input when the last line has no newline, which `set -e` turned
    into a silent exit. An `ERR` trap now names the failing line.
  - Killing `docker-compose run` doesn't stop its container. The trap now removes the k6
    container by name.
- The `k6` service sits under `profiles: [load]`, so `make up` never starts it.
