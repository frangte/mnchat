# T09: Summary button and card in the thread panel

**Status:** blocked · **Size:** S · **Depends on:** the HTTP contract only, testable with mocks · **Blocks:** demo

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

- [x] The thread panel looks the same as today for threads under 3 replies.
- [x] Clicking generates a summary. A thread with a cached summary shows it immediately on open.
- [x] A new reply from another user shows `Update summary` without a reload.
- [x] The button can't double-submit while generating.
- [x] With `search` down: a cached summary still shows, and generating shows the inline error.
- [ ] Works on mobile, reachable by keyboard, and `aria-live="polite"` announces the result.
- [x] `npm run build` passes.

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

Built and browser-tested. One acceptance box stays open, and everything depending on the
backend still needs a real run once T07 is merged.

**Open box:** mobile (Chromium emulation with touch) and keyboard are verified. For
`aria-live`, the result lands in a live region that already exists, and Chromium's accessibility
tree exposes it as `polite` with the summary inside. No screen reader was run, so "announces"
is unconfirmed. One VoiceOver pass closes it.

**Verified only against mocks:** both summary endpoints, so generating, caching, `stale` and the
503 states. The mock derives `stale` from the real replies endpoint, and the replies, threads
and WebSocket events were real. With `search` down, the real path hasn't been exercised.

**Verified for real:** threads under 3 replies match commit 8d283ad exactly, in markup and
pixels, at 1280x800 and 390x780, on the same data.

**Until T07 merges:** `GET` returns a plain-text 404, which correctly reads as no summary.
`POST` also returns 404, so clicking `Summary` shows `Not Found` inline.

**Design notes:**
- 404 becomes `null` in the query function, so "no summary" is a success state.
- `onSuccess` cancels queries before writing, because a slower `GET` would overwrite the result.
- `useIsMutating` plus a synchronous `isMutating` check stops double-submits. `disabled` alone
  doesn't, because React Query notifies through `setTimeout(0)`: a plain double click sent two
  `POST`s without the check. It also keeps the state across closing and reopening the thread.
- Non-503 errors show the server's message, since `POST` can return 404 while the replica lags.

**Known limits:**
- If a reply commits after the server computes `stale` but its socket event beats the `POST`
  response, the card reads as fresh until the next reply or reopen. The window is milliseconds.
- `Summarizing…` uses Composer's `disabled:opacity-40`, about 2:1 contrast. It was kept to match
  the disabled Send button. `disabled:opacity-70` would give about 3.6:1 if the owner wants it.

Merge review on 2026-11-01: the generating button uses `disabled:opacity-70` rather than Composer's
`opacity-40`, because 1.97:1 contrast is too faint for a loading state in a live demo. The one open
box is `aria-live` under a real screen reader, which needs the owner to run VoiceOver. Everything
backend-dependent was mocked, so re-check it end to end after T07.
