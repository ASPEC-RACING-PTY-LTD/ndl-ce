# Claude Agent

Claude Agent runs Claude Code in a system container of its own and connects
it to your Claude account with Remote Control, so you start chats on
[claude.ai/code](https://claude.ai/code) or in the Claude app. It only
reaches what you grant it in No-dal: chosen game server folders, and per
server, reading the log, running console commands and restarting.

Turn it on in Add Features, then open Infrastructure > Claude Agent.

## Set up

1. **Set up agent** creates a system container named `claude-agent`
   (2 vCPU, 2 GB memory, 16 GB disk, Debian 13) and installs Claude Code in
   it as the user `claude`. It takes a few minutes; the page shows progress.
2. **Sign in.** Get a sign-in link, approve it with your Claude account and
   paste the code the page shows. Remote Control needs a Claude plan that
   includes Claude Code (Pro or Max). The sign-in is stored in the
   container only.
3. **Access.** Tick a game server to share it. Garry's Mod servers start
   with `garrysmod/gamemodes` and `garrysmod/addons`, read and write. Add
   or remove folders with the folder picker, make any of them read only,
   and choose whether the agent may read the server log, run console
   commands or restart the server. Save.

The agent then appears on claude.ai/code and in the Claude app under the
name you chose (No-dal by default).

## What the agent can reach

- **Folders.** Each shared folder is mounted into the container at
  `/srv/<server>/<folder>`, for example `/srv/jvs/garrysmod/addons`.
  Nothing else on the host is visible. A read-only folder is mounted read
  only.
- **File ownership is unchanged.** The game keeps owning its files. No-dal
  adds a POSIX ACL for the container's user to the shared folder (and a
  default ACL, so new files inherit it); taking access away removes it
  again. A folder that is a symlink leading outside the server is refused.
- **The `ndl` command.** Inside the container, `ndl servers`,
  `ndl log SERVER [LINES]`, `ndl console SERVER COMMAND` and
  `ndl restart SERVER` call No-dal with a token that only allows what you
  granted. The token changes every time settings or access change.
- **`/srv/CLAUDE.md`** tells Claude where each folder is and which `ndl`
  commands it may use. It is rewritten on every change.

The container reaches this node's control plane on its bridge address and
reaches claude.ai over the network; it has no other way into No-dal.

## Settings

- **Name on claude.ai** names the Remote Control session.
- **Permissions in chats**: ask before edits and commands (default), edit
  files without asking, or plan only.

Changing settings restarts Remote Control. Changing shared folders
restarts the container for a few seconds, which ends open chats.

## Remove

**Remove agent** takes the agent's access away from every folder, then
deletes its container and the Claude sign-in in it. Game server files are
not touched. Claude Agent cannot be turned off in Add Features while its
container exists.
