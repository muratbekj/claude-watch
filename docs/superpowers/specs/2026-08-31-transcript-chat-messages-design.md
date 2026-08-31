# Transcript-derived chat messages on the watch

## Problem

The watch currently only ever shows two kinds of activity: structured tool
calls (via `PostToolUse`/`PermissionRequest` hooks) and, for bridge-spawned
sessions, the raw PTY terminal stream (`pty-output`) — Claude Code's full TUI
output including box-drawing characters, spinners, and redraws. Neither shows
the actual plain-text content of what Claude said when it isn't calling a
tool (a plan summary, a question, a plain answer). Users can't see or
meaningfully respond to ordinary conversational turns from the watch — they
have to go back to the laptop/phone to read what was actually said.

Goal: surface Claude's actual assistant text on the watch as clean chat
lines, so a bridge-spawned session can be followed and replied to (via the
existing voice/text input) without needing the laptop or phone.

## Constraint

True low-latency reply-and-continue (type on the watch, see the response
stream back, no relaunch per message) only works for sessions the **bridge
itself spawned** — it owns that process's stdin directly
(`spawnInteractiveProcess` in `pty.go`, `WriteStdin` in `session.go`).
Sessions merely *detected* via hooks in an arbitrary terminal (e.g. a normal
interactive Claude Code session someone happens to be running) have no such
handle; a reply to one of those spawns a detached one-shot
`claude -p "..." --continue` that resumes the same conversation history but
exits after one turn. This design targets bridge-spawned sessions as the
primary case. Hook-detected sessions still get whatever transcript-derived
messages surface from their headless replies, but the "live chat" experience
is specifically a bridge-spawned-session feature.

## Design

### Data source: Claude Code's transcript file

Every Claude Code hook payload includes `transcript_path`, pointing at that
session's JSONL conversation log. Verified against a real transcript file in
this repo's `~/.claude/projects/...` — each line is a JSON object; the ones
relevant here look like:

```json
{"type": "assistant", "message": {"role": "assistant", "content": [
  {"type": "text", "text": "..."}
]}}
```

`content` is an array of blocks; a block's `type` is one of `"text"`,
`"tool_use"`, or `"thinking"`. A single assistant turn can contain multiple
blocks (e.g. `thinking` + `text`, or just `tool_use` with no text at all).
This is clean, structured data — no TUI rendering noise — regardless of
whether the session is bridge-spawned or hook-detected.

### Bridge: new transcript reader

New file `skill/bridge/transcript.go`:

```go
// LatestAssistantText scans transcriptPath backward for the most recent
// "assistant" entry that has at least one "text" content block, and
// returns the concatenation of that entry's text blocks. Returns ""
// (no error) if the file is missing, empty, or has no such entry —
// this is a best-effort read, never a hard failure.
func LatestAssistantText(transcriptPath string) string
```

Implementation notes:
- Read the file, split into lines, scan from the **end** backward (transcripts
  can be long; we only care about the most recent turn).
- Skip lines that fail to parse as JSON (the file can be mid-write when a
  hook fires) or whose `type` isn't `"assistant"`.
- Stop scanning as soon as one qualifying entry is found (its `content` array
  has ≥1 block with `type == "text"`); join that entry's text blocks with
  `"\n"`. If a scanned assistant entry has content but no `text` block
  (e.g. pure `tool_use`), keep scanning further back — do not treat it as
  "no message", since the most recent turn before it may have had text.
- Cap the scan at the last ~200 lines for efficiency; a transcript without a
  qualifying entry in that window returns "".

### Bridge: wiring into the Stop/Notification handler

`hooks.go`'s `handleHookStop` (already the target of both `Stop` and
`Notification` hooks per `setup-hooks.sh`) additionally:

1. Reads `transcript_path` from the hook body.
2. Calls `LatestAssistantText`.
3. If non-empty **and** different from that session's last-emitted message
   (new `Session.lastMessage string` field in `session.go`, guarded by the
   registry's existing mutex), updates `lastMessage` and pushes a new SSE
   event: `br.sse.PushEvent("message", jmap{"text": text}, &sid)`.
4. The existing `"stop"` event push (unchanged, still carries `message` for
   `Notification`'s idle_prompt/permission_prompt system text) continues to
   fire exactly as it does today — no change to that path.

The dedup on `lastMessage` matters because `Stop` and `Notification` can both
fire for the same turn end; without it the same text would be pushed twice.

### Watch: rendering

`WatchViewState.processEvent` gains a `"message"` case: appends the text as
a new `TerminalLine` with a new `LineType.assistant` case — rendered in
`SessionView.terminalLine(_:)` as a plain, readable chat line (regular
weight, `Theme.Text.primary`, full wrap via `.fixedSize`, no truncation) —
visually distinct from both tool-call cards (`.action`) and the urgent
inline notification (`.notification`).

`"pty-output"` handling in `processEvent` stops appending a `TerminalLine`
for bridge-spawned sessions — the raw text no longer renders in the feed.
(Hook-detected sessions never receive `pty-output` at all — only
bridge-owned PTYs emit it via `bindPtyLifecycle` in `session.go` — so this
change has no effect on them.) The event is still received (harmless no-op)
in case future liveness/connection signals want to key off it; nothing
depends on that today.

### Reply path

Unchanged. `sendVoiceCommand` → `POST /command` → for a session with a live
PTY, `WriteStdin` (immediate injection into the running process); for a
hook-detected session, the existing headless `claude -p "..." --continue`
fallback. Either way, the response naturally flows back through the new
transcript-derived `"message"` pipeline above — no special-casing needed for
"this was a reply."

### Error handling

- Missing/unreadable transcript file, or no qualifying entry in the scan
  window: `LatestAssistantText` returns `""`; the caller treats that as "no
  new message this turn" and simply skips the `"message"` push. Never blocks
  or fails the hook's HTTP response.
- Malformed individual JSON lines (partial write mid-scan): skipped
  individually, do not abort the scan.

### Testing

No automated test suite exists for the bridge or the watch app (per
`CLAUDE.md`); verified manually:
1. Spawn a bridge-owned session (`/claude-watch` or `POST /command
   spawn:"claude"`).
2. Have it reply with plain text (no tool call) — confirm a `"message"` SSE
   event fires with the right text and it renders in the feed.
3. Reply from the watch via the mic button — confirm the reply reaches
   stdin and the next response also surfaces through the same `"message"`
   path.
4. Confirm a turn that only calls a tool (no text) does not push a spurious
   empty `"message"` event, and does not accidentally surface stale text
   from an earlier turn (covered by scanning until a qualifying entry, not
   just checking the last line).

## Out of scope

- Any change to how hook-detected (non-bridge-spawned) sessions handle
  replies — still the existing one-shot `-p --continue` behavior.
- Replaying transcript history from before the watch connected (a separate,
  previously-discussed bridge-buffer-replay improvement — not part of this
  change).
- Rendering `thinking`-type transcript blocks on the watch (only `text`
  blocks are surfaced; the existing whimsical-verb thinking indicator
  already covers "Claude is working" during a turn).
