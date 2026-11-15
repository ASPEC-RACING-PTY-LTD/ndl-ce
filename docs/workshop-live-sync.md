# Live Workshop sync (Garry's Mod)

New and updated items in a server's Workshop collection go live without a
restart. ndl-ce checks Steam and downloads the packs; an addon on the game
server mounts them and staff send them to players. The game server never
contacts Steam for this.

Turn it on per server under **Settings → Configuration → Live Workshop
sync**. It is off by default and only offered for Garry's Mod servers. The
collection is the server's **Workshop collection** setting (or a collection
added as content).

## Folder

Everything is in the server's `garrysmod/data/cs_sync/`:

| File | Written by | Purpose |
| --- | --- | --- |
| `request.json` | server | Staff pressed "Check now" |
| `manifest.json` | ndl-ce | Packs ready to mount |
| `status.json` | server | What the server did with each item |
| `items/<wsid>.gma` | ndl-ce | The packs |

Both sides write a temporary file in the same folder (`name.tmp`) and rename
it over the real one. A reader that cannot parse a file waits a second and
tries again. ndl-ce never follows a link inside this folder.

## When ndl-ce checks

- Every hour while the server runs.
- When `request.json` has an `id` it has not handled. ndl-ce looks every
  5 seconds.
- **Check now** in the panel queues the same kind of request.

At most one check runs per 5 minutes, whatever asked for it. A request inside
that window is answered by the next allowed check. A request `id` is never
handled twice. ndl-ce leaves `request.json` in place.

```json
{ "version": 1, "id": "1731700000-4821", "type": "check",
  "requested_by": "76561198202491846", "requested_name": "Skila", "at": 1731700000 }
```

`id` is 1–80 characters from `A-Z a-z 0-9 . _ : -`. `type` is `check` or
absent.

## The check

1. Read the collection and each item's `time_updated` from the Steam Web API.
2. Work out what the server runs:
   - With a `status.json`: every item it lists that is not `failed`, at its
     `time_updated`.
   - Without one: the collection as it was when the server started. ndl-ce
     notes the versions within seconds of each start.
   - Plus every version ndl-ce has already delivered.
3. Decide what to fetch:
   - An item is `new` if the server has no version of it.
   - An item is `updated` if Steam has a newer `time_updated` than the server.
   - Only those are downloaded, with SteamCMD, anonymously.
4. Convert each download to a plain GMA:
   - A legacy compressed download is unpacked.
   - The GMA's file table is checked against its length.
5. Store and describe each pack:
   - It is saved as `items/<wsid>.gma`, keeping only the latest version.
   - It is labelled `lua` if any file in it ends in `.lua` (gamemodes keep
     theirs outside `lua/`), otherwise `assets`.

Items are skipped and listed in `errors` when they are:
- blocked: `1485877786` (backdoored), and every ID the server has listed in
  `status.json` `blocked`;
- no longer on Steam, or banned;
- not a Garry's Mod item;
- over 4 GiB.

## `manifest.json`

```json
{
  "version": 1, "seq": 42, "generated": 1731700123,
  "request_id": "1731700000-4821", "collection": "3520969473",
  "items": [
    { "wsid": "3839826640", "title": "[TS] Props 2", "state": "updated", "kind": "assets",
      "time_updated": 1731699000, "size": 123456789, "sha256": "…", "file": "items/3839826640.gma" }
  ],
  "errors": [ { "wsid": "1234567890", "error": "download failed: Timeout" } ]
}
```

- **`seq`** goes up by one on every write and never resets. ndl-ce keeps it
  in its own records.
- **`request_id`** is the request this check answered, or `null` for an
  hourly check.
- **`items`** lists every delivered pack the server has not yet acknowledged,
  not only the ones this check fetched. A pack counts as acknowledged once
  `status.json` has an entry for that `wsid` at that `time_updated`, in any
  state. So a manifest the server missed is never lost, and an empty list
  means there is nothing to do.

After writing it, ndl-ce runs `cs_sync_ready <seq>` on the server console.
The server should also re-read the manifest every minute, in case it missed
the command while starting.

## `status.json`

```json
{
  "version": 1, "seq_seen": 42, "updated": 1731700200, "blocked": ["1485877786"],
  "items": [
    { "wsid": "3839826640", "time_updated": 1731699000, "state": "mounted", "detail": "" },
    { "wsid": "3839876298", "time_updated": 1731000000, "state": "in_collection", "detail": "" }
  ]
}
```

| `state` | Meaning |
| --- | --- |
| `mounted` | On the server, not yet sent to players |
| `sent` | Players have been told to download it |
| `failed` | Mount failed or the checksum did not match (`detail` says why) |
| `lua_restart` | The pack has Lua; the server only loads it at a restart |
| `in_collection` | Loaded from the Workshop at startup |

At startup the server should list every collection item it loaded as
`in_collection`, at its `time_updated` (`engine.GetAddons()` reports both).
That list is the truth ndl-ce compares against.

ndl-ce reads only Workshop IDs made of digits from this file. It may add IDs
to its blocklist from it, never remove them.

## Cleanup

ndl-ce deletes `items/<wsid>.gma` once `status.json` shows that item
`in_collection` at the delivered `time_updated` or later. A newer version
replaces the file. A file the server has not marked `in_collection` is never
deleted.

## Unchanged

Starting and restarting a server works as before: the game server downloads
the collection itself at startup. Live sync only adds delivery between
restarts.
