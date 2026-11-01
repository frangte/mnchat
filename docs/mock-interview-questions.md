# Mock interview questions

Practice questions for the ANDPAD Vietnam senior backend interview, answered from MNChat.

## How to use this

- Read a question's notes, close the file, and answer out loud in your own words. The bullets
  are points to hit, not lines to read.
- Bring up the weak spot yourself. It sounds better from you than from the interviewer.
- All you know about ANDPAD is the public job posting. Say "the posting lists Elasticsearch",
  not "you use Elasticsearch for X". If you want to know how they use it, ask.
- MNChat is a demo. Nothing here has run in production, so don't talk about it as if it had.

## What's built

A bullet tagged with a task, like `(T06)`, describes that task's design. Before you answer, check
that task below. If it isn't built, say "the design does X", not "it does X". This table is the
only part of the file that should need updating as work lands.

| Area | Status on 2026-11-01 | Task |
|---|---|---|
| Threads | Built | none |
| gRPC contract and import boundary test | Built | T00 |
| Private-channel WebSocket fix | Built | T02 |
| MySQL replica, views, `search_reader` | Built | T03 |
| Metrics and `readyz` | In progress | T01 |
| Search and summary UI | In progress | T08, T09 |
| `search` service, text search | Planned | T04 |
| Summaries and semantic search | Planned | T05, T07 |
| Chat search endpoint, deadlines, breakers | Planned | T06 |
| Prometheus, Grafana, alerts | Planned | T10, T12 |
| Load test and chaos test | Planned | T13 |

## About the numbers

- **Measured numbers** come from `docs/tasks/VERIFIED-FACTS.md` or a task's Resume notes. They
  are small laptop tests. Say so.
- **Design settings** are choices, not results: the 800 ms deadline, the breaker's 5 failures,
  the 300 ms alert. They come from `docs/tasks/CONTRACTS.md` and the T12 alert table. Never
  present one as something you measured.
- **`[fill in after T13]`** marks a measurement that doesn't exist yet. Replace it with the real
  result, or leave the claim out. Don't estimate.

---

## 1. Search reads chat's data through a replica. Isn't that a shared database? How do you defend it?

**What they're checking:** Whether you admit the coupling and can say what you got for it.

**Answer notes:**
- Yes, in a real sense it is. `search` has no data of its own and depends on chat's tables
  through a copy. Say that first, rather than argue about the label.
- What I limited is how much it shares. `search` reads three `search_*_v1` views. `search_reader`
  holds exactly three `SELECT` grants, and reading a base table fails with error 1142.
- The views are the contract. Chat can change its tables if the views keep their shape. A breaking
  change means a new `_v2` view next to `_v1`, not an edit to `_v1`.
- Writes stay simple. `api` is the only writer, and the replica runs `super_read_only`, so even
  root can't write there. `search` hands summaries back to `api`, which stores them on the primary.
- What I got for the coupling: no event pipeline, no dual writes, and no second data store to run.
  Replication also gives me catch-up after an outage without writing any code.
- The code boundary is enforced. `internal/archtest` fails if either side imports the other. It has
  little to check until `internal/search` exists (T04).

**Weak spot:** Views reduce the coupling, they don't remove it. A chat migration that drops a
column a view uses breaks search, and nothing blocks that migration. Search can't shape data for
its own queries either. The `FULLTEXT` index is on `messages`, so the primary maintains it on every
insert even though only search needs it. With two teams and a real product, I'd treat this as a
first step and expect search to own its data later.

**Likely follow-up:** A chat engineer wants to rename `messages.body` tomorrow. What has to happen?

---

## 2. Elasticsearch is on your stack list. Why didn't you use it, and when would you move to it?

**What they're checking:** Whether you choose tools by fit, and can respect one you left out.

**Answer notes:**
- Elasticsearch is the better search engine. I left it out because it was too much for this demo,
  not because MySQL is better at search.
- The demo needs to prove that a second service can fail without hurting chat. Elasticsearch would
  add a cluster to run and a pipeline to feed it, and neither helps prove that.
- MySQL `FULLTEXT` on the replica covers what I need. In testing, `+deploy*` matched "deploy" and
  "deployment", and `+release* +planning*` found only the row with both words.
- I'd move for Japanese or Vietnamese text, for relevance tuning, for facets or highlighting, or
  when `FULLTEXT` can't keep up with the data size. For a product used in Japan, language alone
  could decide it.
- The move stays small on the chat side. `search` returns IDs and scores, and `api` owns permissions,
  so the store can change behind the gRPC contract without touching `api` or the UI.
- The real work would be feeding the index: a backfill, a way to keep it current, and comparing
  results before switching. Then I'd ask what they use it for, because I don't know.

**Weak spot:** Language is the biggest gap. The default `FULLTEXT` parser splits on spaces and
punctuation, so it won't work for Japanese. MySQL has an `ngram` parser, but I cut it and never
tested it. So this design has no answer yet for Japanese search. There's also no relevance tuning,
and how required stopwords like `+how* +to*` behave is untested until T04.

**Likely follow-up:** How would you backfill Elasticsearch and switch to it with no downtime?

---

## 3. Prove that search failing can't affect chat. What exactly protects chat?

**What they're checking:** Whether "isolated" means specific mechanisms and a test, not a diagram.

**Answer notes:**
- At the database, replication is asynchronous and semi-sync stays off, so the primary never waits
  for the replica. 500 inserts took 1.0 to 1.3 ms each with the replica up, and 0.9 ms with it
  killed. No slowdown.
- In the real repo, with the replica killed, 50 chat posts over HTTP had zero errors. It caught up
  with no manual step.
- At startup, `api` has no Compose dependency on the replica. The gRPC client connects lazily, and
  `readyz` checks only the primary. (T01, T06)
- On a request, only the search and summary routes call `search`. Each RPC has a deadline and a
  breaker. A failure becomes a 503, and the UI says search is unavailable and chat still works.
  (T06, T08)
- The proof is `make chaos-search`. It stops `search`, kills the replica under k6 chat traffic, and
  fails on a single chat error. Chat errors: `[fill in after T13]`. Chat p95 during the outage:
  `[fill in after T13]`. (T13)
- A test also has to be able to fail, so T13 makes `api` depend on `search` for a moment and
  confirms the test goes red. (T13)

**Weak spot:** "Can't" is too strong, and I'd say so. Chat doesn't need search to run, but they
aren't fully apart. A search request still runs membership and channel queries on the primary, even
with the breaker open, and it loads hits from there too. Nothing rate-limits that. Summary
calls can hold an `api` goroutine for up to 60 s. And this covers search only: chat itself has one
`api` process and one primary. The insert numbers are one small laptop test.

**Likely follow-up:** What if `search` isn't down, just slow?

---

## 4. How do you stop a user finding messages in a channel they can't read? Across tenants?

**What they're checking:** Whether permissions live in one place, and what happens when a layer has a bug.

**Answer notes:**
- `api` owns permissions, and `search` keeps no copy. `api` checks workspace membership, then gets
  the readable channel IDs from the primary. A non-member gets 404, so nothing reveals what exists.
  (T06)
- `search` only looks where it's told. `channel_ids` is required, and an empty list returns no hits,
  never "all". Every query also filters by `workspace_id`. (T04)
- There's a second check. `search` returns IDs only, and `api` loads the messages from the primary
  and drops anything outside the readable set. A filter bug in `search` still can't return private
  text. (T06)
- Semantic search follows the same rule, with vectors loaded only for readable channels. Carol must
  never get the `#leadership` thread, even with a query matching it closely, and a test covers
  that. (T05, T07)
- At the database, the views expose only what search needs. They have no emails or password hashes.
- A real bug from this project: while testing threads, a non-member's socket received the full text
  of a private reply, because `Hub.Publish` sent every event to the whole workspace. Users couldn't
  reach it yet, since the app can't create private channels, but seeding one would have exposed it.
  So I fixed it first. Events now carry an audience, and a failed member lookup publishes nothing
  rather than falling back to everyone. A live test with four real sockets passed 23 of 23 checks.

**Weak spot:** `search` trusts `api` completely. The gRPC link has no TLS and no caller auth (T06),
so anything that reaches port 9190 can ask for any channel. Tenancy is a shared schema with a
`WHERE` clause, and `search_reader` can see every workspace's rows. The replica is a full copy, so
the grant limits that user, not the server. Login is a demo login that takes a user ID with no
password. This shows how I design authorization. It isn't a hardened system.

**Likely follow-up:** Someone is removed from a private channel while their socket is open. What do
they still receive?

---

## 5. Replication lag: what can a user see, and what did you do about it?

**What they're checking:** Whether you traced what lag does to a user, not just that it exists.

**Answer notes:**
- Chat never reads the replica, so lag only touches search and summaries. A new message shows in
  the channel at once, but may not be searchable for a moment. I accepted that.
- Results are loaded from the primary. `search` gives IDs and `api` fetches the text, so a lagging
  replica can't show old text. The app has no edit or delete yet, but a hit on a deleted message is
  dropped the same way. (T06)
- Permissions come from the primary too, so lag can't extend anyone's access.
- Summaries carry a `stale` field, true when the primary has a reply newer than `last_msg_id`.
  A summary can be stale the moment it's written, so the UI trusts the field, not the timing. (T07)
- A brand-new thread may not be on the replica yet. `search` returns `NOT_FOUND` and the user gets
  a 404, rather than a summary of half a thread. (T05, T07)
- The replica came back after missing 1,000 rows and caught up with no manual step. `make
  replica-status` shows health, and an alert fires at 30 s of lag for 2 minutes (T12). Worst lag
  under load: `[fill in after T13]`.

**Weak spot:** Any lag number I quote comes from a laptop load test, not real traffic. Search has
no read-your-own-writes, so you can post
a message, search for it right away, and miss it, and the UI won't say search is behind. Binlogs
default to 30 days, so a replica offline for longer can't catch up and needs a rebuild from a backup.
I haven't tested that. A fresh replica builds from init scripts today only because the primary still
has every binlog.

**Likely follow-up:** The replica falls far behind during a busy period. Do you tell users, and how?

---

## 6. How would you extract a service from a Rails monolith into Go without a big-bang rewrite?

**What they're checking:** Whether you can move in small, reversible steps, and know where it gets hard.

**Answer notes:**
- Start with the limits of your answer. The posting mentions refactoring products for microservices,
  but I don't know their codebase. MNChat is Go on both sides, so this is how I'd approach it. If
  you've done this at work, tell that story instead.
- Pick a first piece that's mostly reads, has a clear edge, and can fail without stopping the core
  flow. Search is that kind of piece in MNChat, where failure means a 503 in one panel.
- Leave auth and permissions in the monolith at first, and pass the new service what the caller may
  see, like `channel_ids` here. A second copy of permission logic is how leaks start.
- Start the new service on read-only data behind a small contract. Moving data ownership comes
  later. MNChat stops at this first stage.
- Route traffic gradually behind a flag. Run old and new side by side, compare the results, move a
  small share of traffic, and keep a way back. People call this the strangler pattern.
- Move writes last, one owner at a time, using an outbox or change capture rather than having the
  app write twice. Then delete the old code, or you end up running both.

**Weak spot:** This is my least proven answer. The demo has no Rails, no live traffic, and no writes
moved. Everything after the first read-only stage is a plan I haven't tried. Reading through a
replica is also easy to start and easy to stay on, so it can turn into a shared database for good.

**Likely follow-up:** During the move, Rails and Go both need to write the same table. How do you
keep them consistent?

---

## 7. Why summarize and embed threads rather than each message? What are the limits of cosine in Go?

**What they're checking:** Whether AI choices came from testing, and whether you know where brute force stops working.

**Answer notes:**
- Short replies like "ok thanks" don't embed well alone, and long threads compress well. A summary
  gives one clean piece of text per thread.
- Cost stays under the user's control. Someone clicks [Summary], on a thread with at least 3 replies,
  so there's no background job. (T07, T09) A summary takes about 2 s, or about 4 s cold. Embedding
  takes about 18 ms per query.
- I tested models rather than picking from a list. `bge-m3` got 8 of 8 and `nomic-embed-text` got
  5 of 8. `qwen3:4b` wrote its reasoning into the output, and `qwen2.5:3b` got facts wrong. That's
  4 threads and 8 queries, so it shows the approach works. It isn't a benchmark.
- Cosine is in Go because `DISTANCE()` and `VECTOR_DISTANCE()` don't exist in MySQL Community.
  `search` loads the vectors for the caller's channels and scores all of them. (T05)
- Brute force gives exact results and simple permissions, because it filters to readable channels
  first. The cost grows with data. Each vector is about 4 KB (1024 dimensions at 4 bytes), so every
  1,000 summarized threads adds about 4 MB read per query.
- There's no score cutoff. Matches scored 0.49 to 0.66 and the best non-match 0.38 to 0.54. Those
  overlap, so a fixed cutoff doesn't work, and I show the top few instead. (T05)

**Weak spot:** Only threads someone has summarized can be found by meaning. On a fresh database that's
none of them. With no cutoff, a nonsense query still returns its top results. `gemma3:4b` can get
details wrong, and that error then gets embedded. The prompt injection check was a single "ignore all
previous instructions" message, which isn't a defense. I haven't measured where brute force gets too
slow, and reading `VECTOR` through the Go driver is untested until T05.

**Likely follow-up:** When does cosine in Go stop being acceptable, and what would you move to?

---

## 8. What would you alert on, and how would this translate to Datadog?

**What they're checking:** Whether you alert on what users feel, and can carry the ideas to their tools.

**Answer notes:**
- Start with users. `ChatHighErrorRate` is non-search 5xx over 1% for 2 minutes. `ChatHighLatency`
  is non-search p95 over 300 ms for 5 minutes. Normal chat p95 to compare against:
  `[fill in after T13]`. (T12)
- Then the search path: `SearchServiceDown`, `SearchBreakerOpen`, `ReplicationStopped`, and
  `ReplicaLagHigh` over 30 s for 2 minutes. Chat still works in these cases, so I'd want them seen
  soon, but I wouldn't wake someone by default. (T12)
- Search stays out of chat's numbers. The error panel and alerts exclude the search route. Otherwise
  a search outage looks like a chat outage, which is the opposite of what the design does.
- Labels stay bounded. `route` is `c.FullPath()`, never the raw URL. That matters in Prometheus, and
  more in Datadog, where each tag combination is a billed custom metric.
- The ideas carry over and the queries don't. The Datadog Agent can scrape `/metrics` through its
  OpenMetrics check, or I'd emit through DogStatsD. Rules become monitors, and `mysqld-exporter`
  becomes the MySQL integration. The posting lists Datadog and Bugsnag, so I'd ask how they split
  them.
- I'd also pick up what I cut here. Datadog has tracing, which would help most on the call from
  `api` to `search`.

**Weak spot:** These alerts page nobody. Alertmanager is out of scope, so they only show in
Prometheus and Grafana. The thresholds are guesses until there's real traffic, and with demo traffic
a percentage alert on a few requests is noisy. With no tracing, I'd follow one slow request across
two services by matching metrics by hand. And I haven't run this on Datadog. It's a mapping I
reasoned through.

**Likely follow-up:** Search is down but nobody has complained. Should that wake someone at night?

---

## 9. Walk through deadlines, retries and circuit breakers with gRPC in Go. What would you not retry?

**What they're checking:** Whether you know how these interact, and how retries can make an outage worse.

**Answer notes:**
- Deadlines are per RPC, based on the work each one does. `SearchMessages` gets 800 ms. `SearchThreads`
  gets 2 s because it embeds the query. `SummarizeThread` gets 60 s because it runs a model. The
  request context carries through to Ollama, so a deadline stops that work. (T05, T06)
- There's one breaker per RPC, so a slow model can't block text search. It opens after 5 consecutive
  failures and stays open 10 s, then lets a trial call through. While open, it returns
  `ErrUnavailable` without calling `search`. (T06)
- Only `UNAVAILABLE`, `DEADLINE_EXCEEDED` and `RESOURCE_EXHAUSTED` count as failures.
  `INVALID_ARGUMENT` and `NOT_FOUND` are normal answers from a healthy server. If they counted, one
  user's bad requests could trip the breaker for everyone.
- The design has no automatic retries, on purpose, and the web client sets `retry: false` (T08, T09).
  The user sees a 503 and can try again. Retrying during an outage adds load at the worst moment.
- If I added one, it would be for `SearchMessages` only, on `UNAVAILABLE` only, once, with jitter,
  inside the same 800 ms. It's a read, so a repeat is safe.
- I wouldn't retry `DEADLINE_EXCEEDED`, because the budget is gone. Nor `INVALID_ARGUMENT` or
  `NOT_FOUND`, nor `SummarizeThread`, which ties up the model for seconds. And never `PostMessage`,
  which has no idempotency key, so a retry could post a duplicate.

**Weak spot:** These settings aren't tuned. Search p95 to compare with the 800 ms:
`[fill in after T13]`. Counting consecutive failures misses partial outages: if every other call
fails, the count keeps resetting and the breaker never opens. The summary breaker opens slowly, since
5 failures at 60 s each can take 5 minutes if they arrive one at a time. Cold starts are a risk too.
Summaries take about 4 s cold, and I haven't measured a cold `bge-m3` load. If it takes over 2 s, the
first `SearchThreads` after idle returns a 503, so warm it up before a demo.

**Likely follow-up:** How does a deadline carry across service hops in gRPC, and what if a server
ignores it?

---

## 10. You cut tracing and Elasticsearch from this demo. How do you make scope calls as a tech lead?

**What they're checking:** Whether you cut on purpose, write it down, and know what would change your mind.

**Answer notes:**
- I start with what has to be true. Here, search can die, chat keeps working, and I can show that
  live. Anything that doesn't serve that goes on the cut list.
- That's how these got cut: Elasticsearch, tracing and OpenTelemetry, per-message embeddings, Japanese
  and Vietnamese support, Helm and minikube. A real product might need any of them. None changes what
  the demo proves.
- I write cuts down with the reason, in the Scope decisions table on the task board. Then nobody
  reopens one by accident, and a reader can tell a choice from a miss.
- I run small tests before committing to a plan. They showed `DISTANCE()` isn't in MySQL Community,
  which moved cosine into Go. They rejected two models. And they found that a `GRANT` on a view in an
  init script crashes the primary at startup.
- Each cut leaves a way back. Elasticsearch can go behind the gRPC contract. Tracing can be added at
  the gin and gRPC layers without changing service logic.
- A cut has a cost, and I'd name it. Tracing is the first one I'd bring back if this ran for real,
  since it's most useful once there are two services.

**Weak spot:** These were my calls on my own project. Nobody pushed back: no product manager, no team,
no customer. So it doesn't show the hard part of tech leadership, which is getting people to agree.
Bring one real example from your own work: `[your own example]`. Also expect a challenge on cutting
tracing while showing off two services, and on English only for a product used in Japan. Both are
fair, and it's fine to agree.

**Likely follow-up:** Tell me about a scope call you got wrong. What did you learn?
