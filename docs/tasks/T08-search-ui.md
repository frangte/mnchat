# T08: Search input and results panel

**Status:** todo · **Size:** S · **Depends on:** the HTTP contract only, testable with mocks · **Blocks:** demo

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

- [ ] With no search active, the page looks exactly as it does today, apart from the input.
- [ ] Reuses existing colours, spacing, `Avatar`, `Spinner` and `format.ts`.
- [ ] Only one right-side panel shows. Opening a thread from results replaces search.
- [ ] Clicking a reply result opens the right thread in the right channel.
- [ ] Switching workspace closes the panel.
- [ ] With `search` down, the panel shows the unavailable message and chat keeps working.
- [ ] Keyboard: input, tabs and results are reachable, with visible focus.
- [ ] Mobile: the panel is full screen like `ThreadPanel`, and the input stays out of the header.
- [ ] `npm run build` passes.

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

Not started.
