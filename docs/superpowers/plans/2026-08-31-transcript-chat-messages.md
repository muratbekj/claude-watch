# Transcript-Derived Chat Messages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface Claude's actual plain-text turns (not just tool calls) on the watch by reading Claude Code's transcript file, so a bridge-spawned session can be followed and replied to entirely from the watch.

**Architecture:** A new pure Go function reads the tail of a session's transcript JSONL to find the latest assistant text; the existing `Stop`/`Notification` hook handler calls it and pushes a new `"message"` SSE event (deduped per session); the watch renders that as a plain chat line and stops rendering raw `pty-output` text.

**Tech Stack:** Go (bridge, stdlib only — `bufio`, `encoding/json`, `os`), Swift/SwiftUI (watchOS).

**Spec:** `docs/superpowers/specs/2026-08-31-transcript-chat-messages-design.md`

## Global Constraints

- Never block or fail a hook's HTTP response on transcript-read errors — missing file, malformed JSON, anything: log and treat as "no message", per the spec's Error Handling section.
- No test suite exists for the bridge or the watch app (`CLAUDE.md`) beyond what this plan adds; the one exception is the new `transcript.go`, which is pure-function stdlib Go and gets a real `go test` per the spec — everything else is verified manually (`go build`, `xcodebuild`, curl, the simulator).
- Only `text`-type transcript content blocks are surfaced; `thinking` and `tool_use` blocks are skipped (tool calls are already covered by the existing hook-driven action cards).
- `pty-output` events are only ever emitted for bridge-spawned sessions (`bindPtyLifecycle` in `session.go`) — hook-detected sessions are unaffected by the `pty-output` rendering change in Task 4.

---

### Task 1: Transcript reader

**Files:**
- Create: `skill/bridge/transcript.go`
- Test: `skill/bridge/transcript_test.go`

**Interfaces:**
- Produces: `func LatestAssistantText(transcriptPath string) string` — used by Task 2's `handleHookStop`.

- [ ] **Step 1: Write the failing test**

Create `skill/bridge/transcript_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTranscript(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test transcript: %v", err)
	}
	return path
}

func TestLatestAssistantText_FindsLastTextEntry(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "second reply" {
		t.Errorf("got %q, want %q", got, "second reply")
	}
}

func TestLatestAssistantText_SkipsToolUseAndThinkingOnlyEntries(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"the real answer"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","text":"pondering"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","text":""}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "the real answer" {
		t.Errorf("got %q, want %q", got, "the real answer")
	}
}

func TestLatestAssistantText_JoinsMultipleTextBlocks(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"part one"},{"type":"text","text":"part two"}]}}`,
	})
	got := LatestAssistantText(path)
	want := "part one\npart two"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLatestAssistantText_SkipsMalformedLines(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"good one"}]}}`,
		`not valid json at all {{{`,
	})
	got := LatestAssistantText(path)
	if got != "good one" {
		t.Errorf("got %q, want %q", got, "good one")
	}
}

func TestLatestAssistantText_MissingFileReturnsEmpty(t *testing.T) {
	got := LatestAssistantText("/no/such/file/exists.jsonl")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLatestAssistantText_EmptyPathReturnsEmpty(t *testing.T) {
	got := LatestAssistantText("")
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestLatestAssistantText_NoQualifyingEntryReturnsEmpty(t *testing.T) {
	path := writeTranscript(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","text":""}]}}`,
	})
	got := LatestAssistantText(path)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd skill/bridge && go test ./... -run TestLatestAssistantText -v`
Expected: FAIL — `LatestAssistantText` is undefined (compile error).

- [ ] **Step 3: Write the implementation**

Create `skill/bridge/transcript.go`:

```go
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type transcriptContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type transcriptMessage struct {
	Role    string                   `json:"role"`
	Content []transcriptContentBlock `json:"content"`
}

type transcriptEntry struct {
	Type    string            `json:"type"`
	Message transcriptMessage `json:"message"`
}

// maxTranscriptScanLines caps how many trailing lines LatestAssistantText
// reads, since a long-running session's transcript can grow large and only
// the most recent turn matters here.
const maxTranscriptScanLines = 200

// LatestAssistantText scans transcriptPath backward for the most recent
// "assistant" entry that has at least one "text" content block, and
// returns that entry's text blocks joined by "\n". Returns "" (never an
// error) if the path is empty, the file is missing/unreadable, or no
// qualifying entry exists within the scan window — this is a best-effort
// read used to surface chat content on the watch, never a hard dependency.
func LatestAssistantText(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	lines := make([]string, 0, maxTranscriptScanLines)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > maxTranscriptScanLines {
			lines = lines[1:]
		}
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if text := textFromTranscriptLine(lines[i]); text != "" {
			return text
		}
	}
	return ""
}

// textFromTranscriptLine parses one JSONL line and, if it's an assistant
// entry with at least one non-empty text content block, returns those
// blocks joined by "\n". Returns "" for any other line — wrong type, no
// text blocks (pure tool_use/thinking), or malformed JSON (the transcript
// file can be mid-write when a hook fires).
func textFromTranscriptLine(line string) string {
	var entry transcriptEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return ""
	}
	if entry.Type != "assistant" {
		return ""
	}
	var texts []string
	for _, block := range entry.Message.Content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return strings.Join(texts, "\n")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd skill/bridge && go test ./... -run TestLatestAssistantText -v`
Expected: PASS (all 7 subtests).

- [ ] **Step 5: Build the whole module to catch any integration issue**

Run: `cd skill/bridge && go build -o bridge .`
Expected: builds with no errors.

- [ ] **Step 6: Commit**

```bash
git add skill/bridge/transcript.go skill/bridge/transcript_test.go
git commit -m "feat: add transcript reader for latest assistant text"
```

---

### Task 2: Wire transcript reader into the Stop/Notification hook

**Files:**
- Modify: `skill/bridge/session.go:14-21` (Session struct)
- Modify: `skill/bridge/hooks.go:109-123` (`handleHookStop`)

**Interfaces:**
- Consumes: `LatestAssistantText(transcriptPath string) string` from Task 1.
- Produces: `func (reg *SessionRegistry) SetLastMessageIfChanged(id, text string) bool` — not consumed by any later task in this plan, but follows the same `SessionRegistry` method pattern as `Kill`/`SetRunning` for anyone extending this later.
- Produces: new SSE event `"message"` with payload `{"text": string}` (plus the standard injected `"sessionId"` field every `PushEvent` call adds) — consumed by Task 4's `processEvent`.

- [ ] **Step 1: Add the `LastMessage` field to `Session`**

In `skill/bridge/session.go`, the `Session` struct currently reads (lines 14-23):

```go
type Session struct {
	ID         string
	Agent      string
	Cwd        string
	FolderName string
	State      string // "running" | "ended"
	CreatedAt  time.Time

	// ClaudeSessionID is Claude Code's own stable "session_id" from the
	// hook payload (empty for bridge-spawned sessions and for hook
	// payloads that don't carry one, e.g. Codex). Used to resolve hook
	// events back to the same Session even when the reported cwd changes
	// mid-conversation (e.g. after `cd` in a Bash tool call).
	ClaudeSessionID string

	pty *ptyProcess // nil for externally/hook-detected sessions
}
```

Add a `LastMessage` field right after `ClaudeSessionID`:

```go
	ClaudeSessionID string

	// LastMessage is the most recent transcript-derived assistant text
	// pushed to the watch for this session, used to dedupe when both the
	// Stop and Notification hooks fire for the same turn.
	LastMessage string

	pty *ptyProcess // nil for externally/hook-detected sessions
```

- [ ] **Step 2: Add `SetLastMessageIfChanged` to the registry**

In `skill/bridge/session.go`, add this method near the other small mutation methods (e.g. right after `SetRunning`):

```go
// SetLastMessageIfChanged updates id's last-emitted transcript message and
// reports whether it actually changed. Returns false for an unknown
// session id or a repeat of the same text — used so a transcript-derived
// "message" event isn't pushed twice when Stop and Notification both fire
// for the same turn.
func (reg *SessionRegistry) SetLastMessageIfChanged(id, text string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	s, ok := reg.sessions[id]
	if !ok || s.LastMessage == text {
		return false
	}
	s.LastMessage = text
	return true
}
```

- [ ] **Step 3: Wire it into `handleHookStop`**

In `skill/bridge/hooks.go`, `handleHookStop` currently reads (lines 109-123):

```go
func (br *Bridge) handleHookStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Invalid JSON"})
		return
	}
	sid := br.sessions.ResolveHookSession(body)
	logMsg("info", fmt.Sprintf("Hook: Stop received session=%s", sid))
	br.sse.PushEvent("stop", body, &sid)
	jsonResponse(w, http.StatusOK, jmap{"ok": true})
}
```

Replace it with:

```go
func (br *Bridge) handleHookStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}
	body, err := readBody(r)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Invalid JSON"})
		return
	}
	sid := br.sessions.ResolveHookSession(body)
	logMsg("info", fmt.Sprintf("Hook: Stop received session=%s", sid))

	if transcriptPath, ok := strField(body, "transcript_path"); ok && transcriptPath != "" {
		if text := LatestAssistantText(transcriptPath); text != "" {
			if br.sessions.SetLastMessageIfChanged(sid, text) {
				br.sse.PushEvent("message", jmap{"text": text}, &sid)
			}
		}
	}

	br.sse.PushEvent("stop", body, &sid)
	jsonResponse(w, http.StatusOK, jmap{"ok": true})
}
```

(`strField` is the existing helper already used elsewhere in `hooks.go`, e.g. in `handleHookPermission`.)

- [ ] **Step 4: Build**

Run: `cd skill/bridge && go build -o bridge .`
Expected: builds with no errors.

- [ ] **Step 5: Manually verify against a real hook call**

Start the bridge in one terminal: `cd skill/bridge && ./bridge` (note the printed port, e.g. 7860).

In another terminal, create a fake transcript and POST it as a Stop hook:

```bash
cat > /tmp/fake-transcript.jsonl <<'EOF'
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Here is my plain-text answer"}]}}
EOF

curl -s -X POST http://127.0.0.1:7860/hooks/stop \
  -H "Content-Type: application/json" \
  -d '{"session_id":"verify-task2","cwd":"/tmp","transcript_path":"/tmp/fake-transcript.jsonl"}'
```

Expected: the bridge's log output shows a line like `Hook: Stop received session=...`, and (if you also have an SSE client connected via `GET /events` with a valid pairing token) a `message` event with `{"text":"Here is my plain-text answer",...}` arrives before the `stop` event. Sending the exact same curl command a second time should **not** produce a second `message` event (dedup working) — only the `stop` event repeats.

- [ ] **Step 6: Commit**

```bash
git add skill/bridge/session.go skill/bridge/hooks.go
git commit -m "feat: push transcript-derived message event on turn end"
```

---

### Task 3: Add `.assistant` line type to the watch's terminal line model

**Files:**
- Modify: `ios/ClaudeWatch/Shared/Models/TerminalLine.swift:16-24`

**Interfaces:**
- Produces: `TerminalLine.LineType.assistant` case — consumed by Task 4 (constructing the line) and Task 5 (rendering it).

- [ ] **Step 1: Add the case**

In `ios/ClaudeWatch/Shared/Models/TerminalLine.swift`, the `LineType` enum currently reads (lines 16-24):

```swift
    enum LineType: String, Codable {
        case output      // Claude's output
        case command     // User's command (prefixed with >)
        case system      // System messages (connected, disconnected, etc.)
        case thinking    // Pulsing cursor indicator
        case error       // Error messages
        case action      // A single tool call: icon + title + optional detail
        case notification // Claude is waiting on you — needs a highlighted, hard-to-miss line
    }
```

Add a new case:

```swift
    enum LineType: String, Codable {
        case output      // Claude's output
        case command     // User's command (prefixed with >)
        case system      // System messages (connected, disconnected, etc.)
        case thinking    // Pulsing cursor indicator
        case error       // Error messages
        case action      // A single tool call: icon + title + optional detail
        case notification // Claude is waiting on you — needs a highlighted, hard-to-miss line
        case assistant    // Claude's plain-text reply, read from the transcript
    }
```

- [ ] **Step 2: Verify the watch target still builds**

Run: `cd ios/ClaudeWatch && xcodebuild -project ClaudeWatch.xcodeproj -scheme ClaudeWatchWatch -destination 'platform=watchOS Simulator,name=Apple Watch SE 3 (40mm)' build`

Expected: **BUILD FAILED** — `SessionView.swift`'s `colorFor(_:)` switch over `TerminalLine.LineType` is now non-exhaustive (missing `.assistant`). This confirms the new case is wired into the type; Task 5 fixes the switch.

- [ ] **Step 3: Commit**

```bash
git add ios/ClaudeWatch/Shared/Models/TerminalLine.swift
git commit -m "feat: add assistant line type for transcript-derived messages"
```

---

### Task 4: Handle the `"message"` SSE event and retire `pty-output` rendering

**Files:**
- Modify: `ios/ClaudeWatch/ClaudeWatch watchOS/Services/WatchViewState.swift:182-212` (`processEvent`'s `"stop"` and `"pty-output"` cases)

**Interfaces:**
- Consumes: SSE event `"message"` with `{"text": string, "sessionId": string}` from Task 2; `TerminalLine.LineType.assistant` from Task 3; existing `removeThinkingLine(sessionId:)` and `appendLine(_:sessionId:)`.

- [ ] **Step 1: Add the `"message"` case**

In `ios/ClaudeWatch/ClaudeWatch watchOS/Services/WatchViewState.swift`, `processEvent`'s `case "stop":` block currently reads (lines 182-197):

```swift
        case "stop":
            removeThinkingLine(sessionId: sessionId)
            // Claude Code's Notification hook (idle_prompt/permission_prompt —
            // "Claude is waiting for your input") is routed to this same
            // endpoint and carries a `message`; surface it instead of a
            // generic line so it's obvious a reply is expected.
            if let message = json["message"] as? String, !message.isEmpty {
                appendLine(TerminalLine(text: message, type: .notification), sessionId: sessionId)
                HapticManager.approvalNeeded()
            } else {
                appendLine(TerminalLine(text: "— stopped —", type: .system), sessionId: sessionId)
            }
            isStreaming = false
            if let sid = sessionId, let idx = sessionIndex(for: sid) {
                sessions[idx].activity = .idle
            }
```

Add a new case directly above it (the bridge always pushes `"message"` before `"stop"` for the same turn, so the thinking line must be removed here, not left for the `"stop"` case to find — `removeThinkingLine` only removes the *last* line, and by the time `"stop"` arrives the assistant line would already be last):

```swift
        case "message":
            if let text = json["text"] as? String, !text.isEmpty {
                removeThinkingLine(sessionId: sessionId)
                appendLine(TerminalLine(text: text, type: .assistant), sessionId: sessionId)
            }

        case "stop":
            removeThinkingLine(sessionId: sessionId)
            // Claude Code's Notification hook (idle_prompt/permission_prompt —
            // "Claude is waiting for your input") is routed to this same
            // endpoint and carries a `message`; surface it instead of a
            // generic line so it's obvious a reply is expected.
            if let message = json["message"] as? String, !message.isEmpty {
                appendLine(TerminalLine(text: message, type: .notification), sessionId: sessionId)
                HapticManager.approvalNeeded()
            } else {
                appendLine(TerminalLine(text: "— stopped —", type: .system), sessionId: sessionId)
            }
            isStreaming = false
            if let sid = sessionId, let idx = sessionIndex(for: sid) {
                sessions[idx].activity = .idle
            }
```

- [ ] **Step 2: Stop rendering `pty-output` as a feed line**

The same file's `case "pty-output":` block currently reads (lines 202-212):

```swift
        case "pty-output":
            if let text = json["text"] as? String {
                let cleaned = text.replacingOccurrences(
                    of: "\\x1B\\[[0-9;]*[a-zA-Z]",
                    with: "",
                    options: .regularExpression
                ).trimmingCharacters(in: .whitespacesAndNewlines)
                if !cleaned.isEmpty {
                    appendLine(TerminalLine(text: String(cleaned.prefix(80)), type: .output), sessionId: sessionId)
                }
            }
```

Replace it with:

```swift
        case "pty-output":
            // Superseded by transcript-derived "message" events, which
            // carry Claude's actual reply text without TUI rendering noise
            // (box-drawing, spinners, redraws). Kept as a received-but-inert
            // case rather than removed, in case a future liveness/connection
            // signal wants to key off it.
            break
```

- [ ] **Step 3: Build**

Run: `cd ios/ClaudeWatch && xcodebuild -project ClaudeWatch.xcodeproj -scheme ClaudeWatchWatch -destination 'platform=watchOS Simulator,name=Apple Watch SE 3 (40mm)' build`
Expected: still **BUILD FAILED** on `colorFor(_:)`'s non-exhaustive switch (same reason as Task 3 — fixed next in Task 5). No new errors from this task's changes.

- [ ] **Step 4: Commit**

```bash
git add "ios/ClaudeWatch/ClaudeWatch watchOS/Services/WatchViewState.swift"
git commit -m "feat: handle transcript message events, retire pty-output rendering"
```

---

### Task 5: Render `.assistant` lines in the feed

**Files:**
- Modify: `ios/ClaudeWatch/ClaudeWatch watchOS/Views/SessionView.swift:142-165` (`terminalLine(_:)`)
- Modify: `ios/ClaudeWatch/ClaudeWatch watchOS/Views/SessionView.swift:224-234` (`colorFor(_:)`)

**Interfaces:**
- Consumes: `TerminalLine.LineType.assistant` from Task 3; existing `Theme.Text.primary` design token.

- [ ] **Step 1: Render `.assistant` as a plain chat line**

In `ios/ClaudeWatch/ClaudeWatch watchOS/Views/SessionView.swift`, `terminalLine(_:)` currently reads (lines 142-165):

```swift
    private func terminalLine(_ line: TerminalLine) -> some View {
        if line.type == .action {
            actionCard(line)
        } else if line.type == .thinking {
            Text("\(line.text)…")
                .font(.system(size: 10.5, weight: .medium))
                .foregroundColor(Theme.Text.secondary)
                .modifier(PulseModifier())
        } else if line.type == .notification {
            HStack(alignment: .top, spacing: 4) {
                Image(systemName: "bell.fill")
                    .font(.system(size: 9))
                Text(line.text)
                    .font(.system(size: 11, weight: .semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
            .foregroundColor(Theme.Accent.approval)
            .padding(.vertical, 2)
        } else {
            Text(line.text)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(colorForLine(line))
                .lineLimit(4)
                .truncationMode(.tail)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
```

Add an `.assistant` branch before the final `else` (plain prose, not monospaced — this is chat content, not terminal/code output — full wrap, no truncation, so a real reply is always fully readable):

```swift
    private func terminalLine(_ line: TerminalLine) -> some View {
        if line.type == .action {
            actionCard(line)
        } else if line.type == .thinking {
            Text("\(line.text)…")
                .font(.system(size: 10.5, weight: .medium))
                .foregroundColor(Theme.Text.secondary)
                .modifier(PulseModifier())
        } else if line.type == .notification {
            HStack(alignment: .top, spacing: 4) {
                Image(systemName: "bell.fill")
                    .font(.system(size: 9))
                Text(line.text)
                    .font(.system(size: 11, weight: .semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
            .foregroundColor(Theme.Accent.approval)
            .padding(.vertical, 2)
        } else if line.type == .assistant {
            Text(line.text)
                .font(.system(size: 11.5))
                .foregroundColor(Theme.Text.primary)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.vertical, 2)
        } else {
            Text(line.text)
                .font(.system(size: 11, design: .monospaced))
                .foregroundColor(colorForLine(line))
                .lineLimit(4)
                .truncationMode(.tail)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
```

- [ ] **Step 2: Make `colorFor(_:)` exhaustive again**

The same file's `colorFor(_:)` currently reads (lines 224-234):

```swift
    private func colorFor(_ type: TerminalLine.LineType) -> Color {
        switch type {
        case .output:   return Theme.Text.primary
        case .command:  return .white
        case .system:   return Theme.Text.secondary
        case .thinking: return Theme.Text.primary.opacity(0.5)
        case .error:    return Theme.Accent.error
        case .action:       return .white // unused — actionCard renders its own colors
        case .notification: return Theme.Accent.approval // unused — rendered inline above
        }
    }
```

Add the missing case:

```swift
    private func colorFor(_ type: TerminalLine.LineType) -> Color {
        switch type {
        case .output:   return Theme.Text.primary
        case .command:  return .white
        case .system:   return Theme.Text.secondary
        case .thinking: return Theme.Text.primary.opacity(0.5)
        case .error:    return Theme.Accent.error
        case .action:       return .white // unused — actionCard renders its own colors
        case .notification: return Theme.Accent.approval // unused — rendered inline above
        case .assistant:    return Theme.Text.primary // unused — rendered inline above
        }
    }
```

- [ ] **Step 3: Build**

Run: `cd ios/ClaudeWatch && xcodebuild -project ClaudeWatch.xcodeproj -scheme ClaudeWatchWatch -destination 'platform=watchOS Simulator,name=Apple Watch SE 3 (40mm)' build`
Expected: **BUILD SUCCEEDED**.

- [ ] **Step 4: Commit**

```bash
git add "ios/ClaudeWatch/ClaudeWatch watchOS/Views/SessionView.swift"
git commit -m "feat: render assistant chat lines in the session feed"
```

---

### Task 6: End-to-end manual verification

**Files:** none (verification only — no code changes).

**Interfaces:** none produced; exercises the full pipeline from Tasks 1-5.

- [ ] **Step 1: Rebuild and install the watch app**

```bash
cd ios/ClaudeWatch
xcodebuild -project ClaudeWatch.xcodeproj -scheme ClaudeWatchWatch -destination 'platform=watchOS Simulator,name=Apple Watch SE 3 (40mm)' build
APP_PATH="$(find ~/Library/Developer/Xcode/DerivedData -path '*Debug-watchsimulator/Agent Watch.app' -maxdepth 6 2>/dev/null | head -1)"
xcrun simctl boot "Apple Watch SE 3 (40mm)" 2>/dev/null
xcrun simctl install "Apple Watch SE 3 (40mm)" "$APP_PATH"
xcrun simctl launch "Apple Watch SE 3 (40mm)" com.muratbekj.claudewatch.watchkitapp
```

Expected: app launches to the pairing screen (or reconnects automatically if a valid paired token/bridge session already exists).

- [ ] **Step 2: Start the bridge and spawn a bridge-owned session**

```bash
cd skill/bridge && ./bridge
```

Note the printed pairing code, then pair the watch simulator to it (via the number pad). Then spawn a bridge-owned session so the reply path (`WriteStdin`) is live — e.g. via the `/claude-watch` skill, or:

```bash
curl -s -X POST http://127.0.0.1:<port>/command \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token from pairing>" \
  -d '{"spawn":"claude","cwd":"'"$PWD"'"}'
```

- [ ] **Step 3: Verify a plain-text reply renders as a chat line**

From the spawned session, send a prompt that produces plain text with no tool call (e.g. "what's 2+2, answer in one word"). Confirm on the watch: the thinking indicator (whimsical verb) disappears and is replaced by the answer text rendered as a plain (non-monospaced) line in `Theme.Text.primary` — not a tool-call card, not squeezed into the notification style.

- [ ] **Step 4: Verify a tool-only turn does not produce a spurious message**

Send a prompt that only triggers a tool call with no accompanying prose (e.g. "list files in this directory" if Claude answers purely via a `Bash`/`Read` tool card with no closing remark). Confirm no empty or stale `.assistant` line appears.

- [ ] **Step 5: Verify replying from the watch closes the loop**

Tap the mic button on the watch, dictate or type a follow-up question, send it. Confirm: it appears as a `> ...` command line, then the next plain-text response from Claude appears as a new `.assistant` chat line via the same `"message"` path — no laptop/phone interaction required to see the answer.

- [ ] **Step 6: Confirm no regressions in the existing feed**

Trigger a Read/Edit/Bash tool call and confirm the action-card feed and permission-approval flow (from earlier work) still render and function exactly as before — this task only adds a new line type and retires `pty-output` rendering, it doesn't touch the action-card or approval code paths.
