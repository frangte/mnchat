# T02: Fix private-channel WebSocket leak, seed private channel

**Status:** done · **Size:** S · **Depends on:** none · **Blocks:** private-channel demo

## Goal

Events from non-public channels reach only that channel's members, and the demo has a
private channel to prove it.

## Why

`Hub.Publish` sends every event to the whole workspace. On 2026-10-31 a non-member's socket
received the full text of a private reply. It can't be reached today, because the app can't
create private channels. Seeding one, which the permissions demo needs, makes it reachable,
so this fix has to come first.

## Owns

- Change `internal/domain/domain.go`, `internal/realtime/hub.go`, `internal/realtime/hub_test.go`,
  `internal/service/chat.go`, `internal/service/thread_test.go`, `internal/repo/channel.go`
- Create `internal/infra/mysqldb/migrations/00004_private_channel.sql`

## Steps

1. Add `Audience []int64` to `domain.Event`, tagged `json:"-"`. `nil` means the whole workspace.
2. In `Hub.Publish`, skip clients whose `userID` isn't in `Audience`.
3. Add `MemberIDs(ctx, channelID) ([]int64, error)` to `ChannelRepo` and the `ChannelStore` interface.
4. In `PostMessage`, when `ch.Type != domain.ChannelPublic`, load members **after** any
   auto-join and set `Audience`. If loading fails, don't publish to everyone: log it and
   skip publishing, because the message is already stored.
5. Add migration `00004`:
   - Channel `id=6`, workspace 1, type `private`, name `leadership`, created by user 1.
   - Members: Alice (1) and Bob (2).
   - Two top-level messages about a confidential topic, with a distinctive word such as
     `reorganization` that T04 can search for.
   - A `Down` section that removes all of it.

Don't touch `Sidebar.tsx`. Private channels show `#` for now, to respect the current UI.

## Acceptance

- [x] Hub test: an event with an `Audience` reaches members only.
- [x] Hub test: an event with `Audience == nil` reaches everyone.
- [x] Service test: posting in a private channel sets `Audience`.
- [x] Service test: posting in a public channel leaves `Audience` nil.
- [x] Live test: Carol's socket doesn't receive a message Alice posts in `#leadership`.
- [x] Carol doesn't see `#leadership` in her channel list.
- [x] Bob's socket does receive it, including thread replies.

## Validate

```bash
make test
make api   # applies 00004
```

For the live test, open a WebSocket as Carol to
`ws://localhost:8080/api/v1/ws?workspace_id=1&token=<jwt>` with
`Origin: http://localhost:5173`. Log in with `POST /api/v1/auth/demo-login`, `{"user_id":3}`.
Post as Alice with `POST /api/v1/workspaces/1/channels/6/messages`.

## Gotchas

- The hub's `userID` lookup must stay cheap, since it runs under the hub mutex. Membership
  lookups happen in the service, before `Publish`.
- **Migration order matters.** Goose refuses to start if a lower-numbered migration appears
  after a higher one was applied: `found 1 missing migrations before current version 5`.
  T03 ran first here, so `00004` needed a `make reset-db`.

## Resume notes

Done on 2026-11-01.

- `Event.Audience` is set for non-public channels and tagged `json:"-"`, so it never reaches clients.
- If the member lookup fails, nothing is published. The message is still stored, and members
  see it on their next refetch. It never falls back to a workspace-wide broadcast.
- Live test with four real sockets: 23 of 23 checks passed. Carol and Dave received nothing
  from `#leadership`, members received messages and replies, and public messages still reach everyone.
- Removing the hub filter, or adding a broadcast fallback, makes the new tests fail.
- `#leadership` is channel 6. Its messages contain the word `reorganization` for T04's tests.
- Private channels still show `#` in the sidebar, on purpose.
