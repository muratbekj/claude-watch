# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Agent Watch: control Claude Code (and Codex) sessions from an Apple Watch. Three components talk to each other:

```
Apple Watch <== WCSession ==> iPhone (relay) <== HTTP/SSE ==> Bridge (Node.js, on the Mac)
   (SwiftUI)      sendMessage/                                        |
                 transferUserInfo                          HTTP hooks | PTY stdin
                                                                       v
                                                          Claude Code / Codex session(s)
```

- **`skill/bridge/server.js`** — a single-file Node.js HTTP server. This is the core of the system; almost all behavior lives here.
- **`ios/ClaudeWatch/`** — an Xcode workspace (generated via XcodeGen) with two targets: the iPhone app and the watchOS app, sharing code via `Shared/`.
- **`.claude/skills/claude-watch/`** and **`skill/SKILL.md`** — the `/claude-watch` Claude Code skill definition that starts the bridge.

## Commands

### Bridge server (Node.js)
```bash
cd skill/bridge
npm install          # or: ./skill/setup.sh
node server.js        # or: npm start
```
No build step, no test suite, no linter configured for the bridge — it's a single ESM file (`type: module` in package.json) run directly with `node`.

### Claude Code hooks
```bash
./skill/setup-hooks.sh          # installs global HTTP hooks into ~/.claude/settings.json
./skill/setup-hooks.sh --remove # removes them
./skill/setup-hooks.sh <port>   # if the bridge isn't on the default port 7860
```
This also drops a `~/.local/bin/codex-watch` wrapper script when `codex` is on PATH (Codex has no native hook system, so events are captured by piping `codex exec --json`).

### iOS / watchOS app
```bash
cd ios/ClaudeWatch
xcodegen generate     # regenerate ClaudeWatch.xcodeproj from project.yml (run after adding/removing files)
open ClaudeWatch.xcodeproj
```
Build via Xcode (Cmd+R), selecting the `ClaudeWatch` scheme (iPhone, embeds the watch app) or `ClaudeWatchWatch` scheme (watch only). No CLI build/test commands or test targets exist — verify iOS/watchOS changes by building and running in Xcode/Simulator.

Since `ClaudeWatch.xcodeproj` is generated, **never hand-edit `project.pbxproj`** — add new files/targets/settings to `project.yml` and rerun `xcodegen generate`.

## Bridge server architecture (`skill/bridge/server.js`)

The bridge is a plain `http` server (no framework) with a manual route table (`routes` object mapping `"METHOD /path"` to handlers). Key concepts:

- **Multi-session, multi-agent**: the bridge can host several concurrent `claude` and/or `codex` sessions. Each is a slot in the `sessions` Map (`{ id, agent, cwd, folderName, ptyProcess, state, createdAt }`), keyed by a generated session ID. Binaries are located at startup via `findBinary()` (checks common install paths, then falls back to `which`).
- **Two ways a session gets created**:
  1. **Bridge-spawned**: `POST /command` with `spawn: "claude"|"codex"` — the bridge spawns the CLI itself inside a `script`-wrapped PTY (`spawnInteractiveProcess`) and owns its stdin/stdout.
  2. **Externally detected**: a Claude Code session running in a normal terminal (with hooks installed) or a Codex session — the bridge has no PTY for these, only a session record created on first hook/log event (`resolveHookSession`, `touchExternalSession`). Sending a command to one of these runs the CLI non-interactively (`claude -p ... --continue` / `codex exec ...`) rather than writing to a PTY.
- **Claude Code integration is hook-driven**: `setup-hooks.sh` registers HTTP hooks (`PostToolUse`, `PreToolUse`, `PermissionRequest`, `Stop`, `PostToolUseFailure`, `StopFailure`, `Notification`) pointing at `/hooks/*` routes. `PermissionRequest` is the only blocking one — the handler pushes an SSE `permission-request` event and `await`s `waitForPermission()`, which resolves when `POST /command` later delivers a matching `permissionId` + decision (or times out after 10 minutes). The resolved decision is translated back into the hook's expected JSON response shape, including forwarding `AskUserQuestion` answers via `updatedInput`.
- **Codex integration is poll-driven** (Codex has no hook system): `startCodexMonitor()` runs two pollers on an interval (`CODEX_SESSION_SCAN_INTERVAL_MS`):
  - Tails `~/.codex/sessions/**/*.jsonl` files for new session/tool-call/tool-result events (`scanCodexSessionFiles` → `handleCodexJsonlLine`), synthesizing the same `tool-output`/`session`/`task-complete` SSE events Claude hooks would produce.
  - Tails `~/.codex/log/codex-tui.log` for exec-approval prompts (`scanCodexLog` → `consumeCodexLogChunk`) and synthesizes a `permission-request` SSE event (`surfaceCodexExecApproval`); the response is delivered by writing directly to the Codex PTY's stdin (`resolveCodexSyntheticPermission`), since Codex approvals are answered via keypress, not an HTTP callback.
- **Everything fans out over one SSE stream** (`GET /events`, auth via bearer token from `/pair`). Events carry a monotonic `id` and are buffered in a 500-entry ring (`sseBuffer`) so reconnecting clients can replay via `Last-Event-ID`. New SSE clients are also synced with a snapshot of currently-running sessions and pending permissions on connect.
- **Pairing**: `POST /pair` with a 6-digit code (5-minute TTL, rate-limited to 5 attempts / 5 minutes) issues a bearer token used by `/events` and implicitly trusted by `/command` (no per-request re-auth beyond `requireAuth`).
- Port: tries `7860`–`7869` in order and binds the first free one; advertises itself via Bonjour/mDNS as `_claude-watch._tcp`.

When adding a new tool/event type to the bridge, the general pattern is: emit it as an SSE event with a `source` field (`"claude"` or `"codex"`) and a `sessionId`, and handle it in `RelayService`/`WatchViewState` on the client side (see below) — the terminal rendering and permission UI are driven generically off `tool_name`/`tool_input`/`tool_output`, not hardcoded per event.

## iOS / watchOS app architecture

- **`Shared/`** is compiled into both the iOS and watchOS targets (see `project.yml`): models (`SessionState`, `TerminalLine`, `ApprovalRequest`, `AgentSession`, `WatchMessage`, `OutputRingBuffer`), the `WatchSessionManager` (WCSession wrapper), and shared extensions/icons.
- **`WatchMessage`** (`Shared/Models/WatchMessage.swift`) is the single typed envelope for everything sent over `WCSession` between iPhone and Watch — an `enum` with associated payload structs, serialized to `[String: Any]` for WCSession and reconstructed via a `messageType` discriminator. Add new iPhone↔Watch message kinds here (new case + payload struct + the two switch statements), not as ad-hoc dictionaries.
- **`WatchSessionManager`** (`Shared/Connectivity/WatchSessionManager.swift`) is a singleton `ObservableObject` used identically on both platforms: `send()` prefers `sendMessage` (needs reachability) and falls back to `transferUserInfo` (queued delivery); `updateApplicationContext` is used for latest-value-only state like connection status.
- **iPhone app** (`ClaudeWatch iOS/`): `BonjourDiscovery` finds the bridge on the LAN, `BridgeClient` (HTTP) + `SSEClient` talk to it, and `RelayService` is the coordinator gluing bridge SSE events to `WatchSessionManager` sends (i.e. it's the iPhone-side "relay" in the architecture diagram — largest file in the app at ~700 lines).
- **watchOS app** (`ClaudeWatch watchOS/`): connects to the bridge *directly* over Wi-Fi (not solely through the phone) via `WatchBridgeClient`; `WatchViewState` is the equivalent of `RelayService` on the watch side — owns SSE consumption and view state directly. `SpeechService` handles dictation input, `HapticManager` handles feedback, `MultiSessionPager`/`StatusDashboard` render the multi-session list, `ApprovalView` renders permission/`AskUserQuestion` prompts as dynamic option lists.
- Both agent types (Claude, Codex) are visually distinguished via `AgentIcon`/`ClaudeMascot`/`CodexLogo` in `Shared/Extensions/`.

Note: the top-level `README.md`'s "Project Structure" section predates multi-session/multi-agent support (no mention of `AgentSession`, `MultiSessionPager`, Codex handling in `server.js`) — treat this CLAUDE.md and the actual source as authoritative over that section.
