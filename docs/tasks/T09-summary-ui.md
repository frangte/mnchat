# T09: Summary button and card in the thread panel

**Status:** todo · **Size:** S · **Depends on:** the HTTP contract only, testable with mocks · **Blocks:** demo

## Goal

Add a [Summary] button to threads with at least 3 replies, and show the result in a card.

## Why

The button triggers the AI summary and embedding, which is what makes a thread searchable
by meaning. The owner chose a user-triggered flow rather than a background job.

## Owns

- Create `web/src/components/ThreadSummary.tsx`
- Change `web/src/components/ThreadPanel.tsx`, `web/src/api.ts`

## Steps

1. Add to `api.ts`: a `ThreadSummary` type, `api.summary(wsId, rootId)` and
   `api.summarize(wsId, rootId)`.
2. `ThreadSummary` receives `workspaceId`, `rootId`, `replyCount`.
   - Query `['summary', rootId]`, with `retry: false`. A 404 means "no summary yet", not an error.
     `ApiError` carries `status`.
   - Mutation `api.summarize`. On success, write the result to the query cache.
3. Render between the root message and the replies divider in `ThreadPanel`.
4. States:

   | State | Shows |
   |---|---|
   | `replyCount < 3` | Nothing |
   | No summary | `Summary` button |
   | Generating | Disabled button with `Spinner`, text `Summarizing…` |
   | Has summary | Card with the summary and small muted model name |
   | `stale` | Same card, plus an `Update summary` button |
   | 503 | Inline `Summary is temporarily unavailable.`, button still usable to retry |

5. Style the card with existing tokens: `rounded-xl border border-zinc-200 bg-zinc-50`,
   and the indigo button style from `Composer`.
6. Refetch `['summary', rootId]` when `replyCount` changes, so `stale` updates live.

## Acceptance

- [ ] The thread panel looks the same as today for threads under 3 replies.
- [ ] Clicking generates a summary. A thread with a cached summary shows it immediately on open.
- [ ] A new reply from another user shows `Update summary` without a reload.
- [ ] The button can't double-submit while generating.
- [ ] With `search` down: a cached summary still shows, and generating shows the inline error.
- [ ] Works on mobile, reachable by keyboard, and `aria-live="polite"` announces the result.
- [ ] `npm run build` passes.

## Validate

```bash
cd web && npm run build
```

Then a browser check with two users, as in T08.

## Gotchas

- A summary can take several seconds on a cold model, so the loading state matters in the demo.
- Fresh summaries can be `stale` right away if the replica lags. That's correct, so don't hide it.
- `ThreadPanel` is keyed by root ID, so state resets when the thread changes.

## Resume notes

Not started.
