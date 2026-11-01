# T08: Search input and results panel

**Status:** done · **Size:** S · **Depends on:** the HTTP contract only, testable with mocks · **Blocks:** demo

## Goal

Add a search box in the channel header and a results panel in the right-side slot, with
**Messages** and **Threads** tabs.

## Why

The owner wants the current UI kept. This is one of only two UI additions, so it should look
as if it was always there.

## Owns

- Create `web/src/components/SearchPanel.tsx`
- Change `web/src/pages/ChatPage.tsx`, `web/src/api.ts`

Don't change `MessageList.tsx`, `Sidebar.tsx`, `Composer.tsx` or `index.css`.

## Steps

1. Add `api.search(wsId, q, mode)` and the types `MessageHit`, `ThreadHit`, using the shapes
   in CONTRACTS.md.
2. In `ChatPage`, replace `threadRootId` state with one panel state, so only one shows at a time:

   ```ts
   type Panel = { kind: 'thread'; rootId: number } | { kind: 'search'; query: string } | null
   ```

3. Add a `<form role="search">` to the header at `ChatPage.tsx:103`, pushed right with
   `ml-auto`. Enter submits, and Escape in the input clears and closes. Hide it below `sm`
   and give it the same style as the composer's border and focus ring.
4. `SearchPanel`: use the same `<aside>` classes and header layout as `ThreadPanel`.
   - Tabs: Messages (default) and Threads, with `role="tablist"`.
   - Query keys: `['search', workspaceId, mode, query]`, with `retry: false`.
5. Messages results: author, time, `# channel`, body with `line-clamp-3`.
   Click: switch channel, and if the hit is a reply, open its thread.
6. Threads results: root author, `# channel`, summary, and a small "May be out of date" note
   when `stale`. Click: switch channel and open the thread.
7. States:
   - Loading: `Spinner`.
   - 503: `Search is temporarily unavailable. Chat still works.`
   - Empty Messages: `No messages match "<q>".`
   - Empty Threads: `No matching threads. Only summarized threads can be found here.`

## Acceptance

- [x] With no search active, the page looks exactly as it does today, apart from the input.
- [x] Reuses existing colours, spacing, `Avatar`, `Spinner` and `format.ts`.
- [x] Only one right-side panel shows. Opening a thread from results replaces search.
- [x] Clicking a reply result opens the right thread in the right channel.
- [x] Switching workspace closes the panel.
- [x] With `search` down, the panel shows the unavailable message and chat keeps working.
- [x] Keyboard: input, tabs and results are reachable, with visible focus.
- [x] Mobile: the panel is full screen like `ThreadPanel`, and the input stays out of the header.
- [x] `npm run build` passes.

## Validate

```bash
cd web && npm run build
```

Then a browser check. Chrome isn't installed, but a Playwright Chromium is cached at
`~/Library/Caches/ms-playwright/chromium-1181`. Use `playwright-core@1.54` with `executablePath`.

## Gotchas

- `selectChannel` clears panels and unread, so clicking a result needs to set the channel and
  then the new panel.
- `ThreadPanel` renders nothing if the root isn't in the newest 100 messages. An old result
  won't open its thread. Leave this as a known limit.
- Below `sm` there's no input. That's deliberate, because the header is tight on phones. Search
  on phones is out of scope.
- `ChatPage.tsx` is a hot file. T09 doesn't need to change it, so they can run in parallel.

## Resume notes

Done, and verified in Chromium against the real API with only `/search` mocked, because T06
doesn't exist yet. The mock used real message IDs, so channel, thread and reply loading are real.

**Recheck after T06 merges:** real hits, a real 503, and that empty results render. All of these
have only been tested against mocks.

- Idle pixel diff against a pristine 8d283ad build, using a frozen API snapshot: mobile is
  identical and desktop differs only inside the input.
- The panel handles `items: null` as well as `[]`, because Go encodes a nil slice as null.
- Errors other than 503 show `Couldn't search: <message>`. Until T06 merges, the real route's
  404 lands here.

Choices the task left open:

- A top-level message hit switches channel and closes search, because `selectChannel` clears
  panels. It doesn't scroll to the message, so an older hit can be off screen.
- The input keeps its text after a result opens, so Enter returns to the results. The Threads
  tab resets to Messages when it does. Escape, ✕ and a workspace switch clear the text.
- Message hits show the day as well as the time, because a time alone misleads across days.
- Escape with a thread open clears the input and leaves the thread open.
- Tab from the input jumps to the active tab. In DOM order the panel follows every message's
  buttons, even though it sits right beside the input.
- With a panel open at 768px or a little wider, the header input shrinks to a sliver, as the
  composer already does. It doesn't overflow.

Merge check on 2026-11-01, with T08 and T09 combined: T09's suite passed 57 of 57, and T08's suite
passed apart from 2 checks counting console 404s. Those were all `GET .../messages/1/summary`, which
is how the contract says "no summary yet", so the suite's expectation predates T09. It isn't a bug.
