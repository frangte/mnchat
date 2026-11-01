# T11: Mock interview questions

**Status:** todo · **Size:** S · **Depends on:** none, better after design is settled · **Blocks:** none

## Goal

Create `docs/mock-interview-questions.md` with 10 questions an ANDPAD interviewer would ask a
senior backend candidate, each with answer notes based on this project.

## Why

The demo is for a senior role. The owner needs to defend the design, including its weak spots.

## Owns

- Create `docs/mock-interview-questions.md`

## ANDPAD context

From [the Senior Golang posting](https://andpad.vn/job/senior-golang-engineer-backend-aws-mysql-4391)
and andpad.vn, read 2026-11-01. Team size and internal architecture aren't public, so don't
claim to know them.

- Construction project management SaaS in Japan, with over 684,000 users.
- Stack listed: Go, gRPC, AWS, GCP, Amazon Aurora (MySQL compatible), Elasticsearch, DynamoDB,
  Docker, Kubernetes (nice to have), Terraform, Datadog, Bugsnag. Ruby on Rails also appears in postings.
- Work includes "Refactoring existing products for microservices".
- Teams work as Squads, from design through operation.
- Senior requirements: 5+ years with Go, AWS and gRPC, tech lead experience, RDBMS and KVS
  schema design, query tuning, web security, unit testing, good English.
- Nice to have: architecture and middleware selection, "Context Engineering" for AI agents.
- Values include Technology First, Reality, Customer Success, Professional.

## Questions to cover

1. Search reads chat's data through a replica. Isn't that a shared database? How do you defend it?
2. Why not Elasticsearch, given we use it? When would you move to it?
3. Prove that search failing can't affect chat. What exactly protects chat?
4. How do you stop a user finding messages in a channel they can't read? Across tenants?
5. Replication lag: what can a user see, and what did you do about it?
6. How would you extract a service from a Rails monolith into Go without a big-bang rewrite?
7. Why summarize and embed threads rather than each message? What are the limits of cosine in Go?
8. What would you alert on, and how would this translate to Datadog?
9. Walk through deadlines, retries and circuit breakers with gRPC in Go. What would you not retry?
10. You cut tracing and Elasticsearch from this demo. How do you make scope calls as a tech lead?

Adjust wording freely, but keep the coverage.

## Format for each question

```markdown
## 1. Question text

**What they're checking:** one line.

**Answer notes:**
- 3 to 6 bullets, specific to this project, with real numbers from VERIFIED-FACTS.md.

**Weak spot:** the honest limit of the design.

**Likely follow-up:** one question.
```

## Acceptance

- [ ] 10 questions, each with all four parts.
- [ ] Every number traces to VERIFIED-FACTS.md or real measurements.
- [ ] Question 1 states the coupling trade-off honestly and doesn't dismiss it.
- [ ] Doesn't claim production experience the owner hasn't told us about.
- [ ] Doesn't claim knowledge of ANDPAD's internal systems.
- [ ] Sounds like how a person would answer out loud, in plain English.

## Validate

No code to run. Check each number against VERIFIED-FACTS.md, and read every answer out loud.

## Gotchas

- Facts to use: replica killed, writes didn't slow (0.9 ms vs 1.0–1.3 ms per insert), and 1,000
  missed rows were caught up automatically. Semantic test was only 4 threads and 8 queries,
  so call it a small sample.
- The private-channel WebSocket leak found while testing threads makes a good "how do you find
  bugs" story. Be accurate: it's in T02.

## Resume notes

Not started.
