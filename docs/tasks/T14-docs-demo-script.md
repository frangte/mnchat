# T14: Update docs, write ADRs and the demo script

**Status:** todo · **Size:** S · **Depends on:** everything · **Blocks:** none

## Goal

Bring the docs in line with what was built, and write a rehearsable demo script.

## Why

`docs/design.md` still describes MySQL vectors in the main DB, a nightly embedding worker,
Claude and Voyage, and Helm. An interviewer reading it should see the real system.

## Owns

- Change `docs/design.md`, `README.md`
- Create `docs/adr/0001-search-on-mysql-replica.md`
- Create `docs/adr/0002-thread-level-semantic-search.md`
- Create `docs/adr/0003-metrics-only-observability.md`
- Create `docs/demo-script.md`

## Steps

1. `docs/design.md`: update the architecture, Search, AI, Stack and Build order sections.
   Remove the nightly worker, `message_chunks`, `embedding_state`, Claude, Voyage and Helm,
   or mark clearly what's planned versus built.
2. `README.md`: prerequisites (Ollama and models), `make up`, `make web`, demo users, monitoring URLs.
3. ADRs, under a page each: context, decision, alternatives rejected, consequences.
   - 0001: replica over Elasticsearch and an outbox with a broker. Include the coupling trade-off.
   - 0002: summaries of threads, triggered by users, and cosine in Go.
   - 0003: Prometheus and Grafana now, with tracing a deliberate later step.
4. `docs/demo-script.md`, about 10 minutes, with exact clicks and commands:
   1. Before starting: Ollama running, models warm, seeded summaries read and correct.
   2. Chat works: two users, live messages, a thread.
   3. Search: text results, then a permissions check as Carol.
   4. Summary: click [Summary], then find that thread by meaning.
   5. Failure: Grafana on screen, `make chaos-search`, chat stays flat.
   6. Wrap-up: the trade-offs, and what you'd do next.
   7. If something breaks: a fallback for each step, including a recording.
5. Use the demo script once, end to end, on a fresh `make reset-db`.

## Acceptance

- [ ] Nothing in the docs describes a removed component as current.
- [ ] A new reader can go from clone to a running demo with the README alone.
- [ ] The demo script was run once, and timing was noted.
- [ ] Every seeded summary was read and is factually correct.
- [ ] A backup recording exists, or the owner has been told to make one.

## Validate

```bash
make reset-db && make up
make web
```

Then follow `docs/demo-script.md` exactly.

## Gotchas

- `docs/open-question.md` is the owner's original note. Leave it alone.
- Keep the design doc's existing style: short, plain, and honest about scale-up paths.

## Resume notes

Not started.
