package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session mirrors a slot in server.js's `sessions` Map. Only "claude" is
// a supported agent in this port; the field is kept (rather than removed)
// so the wire format the iOS/watchOS apps already decode stays identical.
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

	// LastMessage is the most recent transcript-derived assistant text
	// pushed to the watch for this session, used to dedupe when both the
	// Stop and Notification hooks fire for the same turn.
	LastMessage string

	// TranscriptPath is this session's Claude Code transcript JSONL file,
	// kept current by ResolveHookSession from every hook payload. Used to
	// backfill recent chat history when a client opens this session.
	TranscriptPath string

	pty *ptyProcess // nil for externally/hook-detected sessions
}

type sessionSnapshot struct {
	ID         string `json:"id"`
	Agent      string `json:"agent"`
	Cwd        string `json:"cwd"`
	FolderName string `json:"folderName"`
	State      string `json:"state"`
	CreatedAt  int64  `json:"createdAt"`
}

func (s *Session) snapshot() sessionSnapshot {
	return sessionSnapshot{
		ID:         s.ID,
		Agent:      s.Agent,
		Cwd:        s.Cwd,
		FolderName: s.FolderName,
		State:      s.State,
		CreatedAt:  s.CreatedAt.UnixMilli(),
	}
}

// SessionRegistry owns the sessions map and pushes SSE events on
// lifecycle transitions, mirroring spawnSession/killSession/etc.
type SessionRegistry struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	sse       *SSEHub
	claudeBin string
}

func newSessionRegistry(sse *SSEHub, claudeBin string) *SessionRegistry {
	return &SessionRegistry{
		sessions:  make(map[string]*Session),
		sse:       sse,
		claudeBin: claudeBin,
	}
}

func newSessionID() string {
	return newUUID()
}

func folderNameFor(cwd string) string {
	name := filepath.Base(cwd)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return cwd
	}
	return name
}

// Spawn starts a new bridge-owned PTY session for the "claude" agent.
func (reg *SessionRegistry) Spawn(agent, cwd string) (*Session, error) {
	id := newSessionID()
	folderName := folderNameFor(cwd)

	logMsg("info", fmt.Sprintf("Spawning %s session %s in PTY (cwd: %s)", agent, id, cwd))

	slot := &Session{
		ID:         id,
		Agent:      agent,
		Cwd:        cwd,
		FolderName: folderName,
		State:      "running",
		CreatedAt:  time.Now(),
	}

	proc, err := spawnInteractiveProcess(reg.claudeBin, cwd, nil)
	if err != nil {
		msg := fmt.Sprintf("Cannot spawn %s: %s", agent, err.Error())
		logMsg("error", msg)
		reg.sse.PushEvent("error", jmap{"error": msg}, nil)
		return nil, err
	}

	slot.pty = proc

	reg.mu.Lock()
	reg.sessions[id] = slot
	reg.mu.Unlock()

	reg.bindPtyLifecycle(slot, proc)

	reg.sse.PushEvent("session", jmap{
		"state": "running", "agent": agent, "cwd": cwd, "folderName": folderName,
	}, &id)

	logMsg("info", fmt.Sprintf("%s session %s started (%s), pid: %d", agent, id, folderName, proc.pid()))
	return slot, nil
}

// bindPtyLifecycle wires stdout/stderr -> SSE pty-output and process
// exit -> SSE session "ended", mirroring bindPtyProcess().
func (reg *SessionRegistry) bindPtyLifecycle(slot *Session, proc *ptyProcess) {
	id := slot.ID
	go func() {
		exitCode, signal, err := proc.streamAndWait(func(text string, _ bool) {
			reg.sse.PushEvent("pty-output", jmap{"text": text}, &id)
		})

		reg.mu.Lock()
		slot.State = "ended"
		slot.pty = nil
		reg.mu.Unlock()

		data := jmap{"state": "ended", "agent": slot.Agent, "folderName": slot.FolderName}
		if err != nil {
			logMsg("error", fmt.Sprintf("Session %s PTY spawn error: %s", id, err.Error()))
			data["error"] = err.Error()
		} else {
			logMsg("info", fmt.Sprintf("Session %s (%s) PTY exited: code=%d signal=%s", id, slot.Agent, exitCode, signal))
			data["exitCode"] = exitCode
			data["signal"] = signal
		}
		reg.sse.PushEvent("session", data, &id)
	}()
}

func (reg *SessionRegistry) Kill(id string) bool {
	reg.mu.Lock()
	slot, ok := reg.sessions[id]
	if !ok {
		reg.mu.Unlock()
		return false
	}
	proc := slot.pty
	slot.State = "ended"
	slot.pty = nil
	agent, folderName := slot.Agent, slot.FolderName
	reg.mu.Unlock()

	if proc != nil {
		proc.terminate()
	}

	reg.sse.PushEvent("session", jmap{
		"state": "ended", "agent": agent, "folderName": folderName, "killed": true,
	}, &id)
	logMsg("info", fmt.Sprintf("Session %s killed", id))
	return true
}

// HasLivePty reports whether id currently has a bridge-owned PTY process.
func (reg *SessionRegistry) HasLivePty(id string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	s, ok := reg.sessions[id]
	return ok && s.pty != nil
}

// WriteStdin writes text to id's live PTY stdin, if it has one.
func (reg *SessionRegistry) WriteStdin(id, text string) error {
	reg.mu.Lock()
	s, ok := reg.sessions[id]
	var proc *ptyProcess
	if ok {
		proc = s.pty
	}
	reg.mu.Unlock()
	if !ok {
		return fmt.Errorf("no session with that ID")
	}
	if proc == nil {
		return fmt.Errorf("session has no live stdin")
	}
	return proc.writeStdin(text)
}

// KillAll terminates every bridge-owned PTY and clears the registry,
// used during graceful shutdown.
func (reg *SessionRegistry) KillAll() {
	reg.mu.Lock()
	sessions := reg.sessions
	reg.sessions = make(map[string]*Session)
	reg.mu.Unlock()

	for id, s := range sessions {
		if s.pty != nil {
			s.pty.terminate()
			logMsg("info", fmt.Sprintf("Killed session %s (%s)", id, s.Agent))
		}
	}
}

func (reg *SessionRegistry) Get(id string) (*Session, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	s, ok := reg.sessions[id]
	return s, ok
}

func (reg *SessionRegistry) FindByClaudeSessionID(claudeSessionID string) *Session {
	if claudeSessionID == "" {
		return nil
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, s := range reg.sessions {
		if s.ClaudeSessionID == claudeSessionID {
			return s
		}
	}
	return nil
}

func (reg *SessionRegistry) FindByCwd(cwd string) *Session {
	if cwd == "" {
		return nil
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, s := range reg.sessions {
		if s.Cwd == cwd && s.State == "running" {
			return s
		}
	}
	return nil
}

func (reg *SessionRegistry) FindMostRecentActive() *Session {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	var best *Session
	for _, s := range reg.sessions {
		if s.State == "running" && s.pty != nil {
			if best == nil || s.CreatedAt.After(best.CreatedAt) {
				best = s
			}
		}
	}
	return best
}

func (reg *SessionRegistry) FindMostRecentRunning() *Session {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	var best *Session
	for _, s := range reg.sessions {
		if s.State == "running" {
			if best == nil || s.CreatedAt.After(best.CreatedAt) {
				best = s
			}
		}
	}
	return best
}

func (reg *SessionRegistry) Snapshot() []sessionSnapshot {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]sessionSnapshot, 0, len(reg.sessions))
	for _, s := range reg.sessions {
		out = append(out, s.snapshot())
	}
	return out
}

func (reg *SessionRegistry) RunningSnapshot() []sessionSnapshot {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]sessionSnapshot, 0)
	for _, s := range reg.sessions {
		if s.State == "running" {
			out = append(out, s.snapshot())
		}
	}
	return out
}

// ResolveHookSession primarily matches by Claude Code's own stable
// "session_id" from the hook payload, so one logical conversation stays
// one Session even as its reported cwd changes (e.g. after `cd` in a Bash
// tool call). Only when the payload carries no session_id (older/other
// hook payloads, e.g. Codex) does it fall back to the old heuristics:
// match by cwd, else the most recent bridge-owned active session, else
// auto-create an externally-detected session slot (agent is always
// normalized to "claude" — Codex support is out of scope for this port).
func (reg *SessionRegistry) ResolveHookSession(body jmap) string {
	claudeSessionID, _ := strField(body, "session_id")
	transcriptPath, _ := strField(body, "transcript_path")

	if claudeSessionID != "" {
		if match := reg.FindByClaudeSessionID(claudeSessionID); match != nil {
			reg.SetTranscriptPath(match.ID, transcriptPath)
			return match.ID
		}
	}

	cwd, _ := strField(body, "session_cwd")
	if cwd == "" {
		cwd, _ = strField(body, "cwd")
	}

	if claudeSessionID == "" {
		if match := reg.FindByCwd(cwd); match != nil {
			reg.SetTranscriptPath(match.ID, transcriptPath)
			return match.ID
		}
		if active := reg.FindMostRecentActive(); active != nil {
			reg.SetTranscriptPath(active.ID, transcriptPath)
			return active.ID
		}
	}

	resolvedCwd := cwd
	if resolvedCwd == "" {
		resolvedCwd = defaultCwd()
	}
	folderName := folderNameFor(resolvedCwd)
	id := newSessionID()

	slot := &Session{
		ID:              id,
		Agent:           "claude",
		Cwd:             resolvedCwd,
		FolderName:      folderName,
		State:           "running",
		CreatedAt:       time.Now(),
		ClaudeSessionID: claudeSessionID,
		TranscriptPath:  transcriptPath,
	}

	reg.mu.Lock()
	reg.sessions[id] = slot
	reg.mu.Unlock()

	logMsg("info", fmt.Sprintf("Auto-created session %s for external claude (%s)", id, folderName))
	reg.sse.PushEvent("session", jmap{
		"state": "running", "agent": "claude", "cwd": resolvedCwd, "folderName": folderName,
	}, &id)

	return id
}

// SetTranscriptPath records the transcript file path for id, if non-empty
// and id is known. Hook payloads carry this on every call, so it's kept
// current without touching every individual hook handler.
func (reg *SessionRegistry) SetTranscriptPath(id, path string) {
	if path == "" {
		return
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if s, ok := reg.sessions[id]; ok {
		s.TranscriptPath = path
	}
}

// GetTranscriptPath returns id's known transcript path, or "" if the
// session is unknown or none has been recorded yet.
func (reg *SessionRegistry) GetTranscriptPath(id string) string {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if s, ok := reg.sessions[id]; ok {
		return s.TranscriptPath
	}
	return ""
}

// SetRunning transitions a hook-detected (PTY-less) session back to
// "running" and announces it, used by the headless one-shot prompt path.
func (reg *SessionRegistry) SetRunning(id string) {
	reg.mu.Lock()
	slot, ok := reg.sessions[id]
	if !ok {
		reg.mu.Unlock()
		return
	}
	slot.State = "running"
	agent, cwd, folderName := slot.Agent, slot.Cwd, slot.FolderName
	reg.mu.Unlock()

	reg.sse.PushEvent("session", jmap{
		"state": "running", "agent": agent, "cwd": cwd, "folderName": folderName,
	}, &id)
}

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

// launchCwdArg mirrors server.js's process.argv[2] fallback — an optional
// working directory passed as the first CLI argument at launch.
var launchCwdArg string

func defaultCwd() string {
	if launchCwdArg != "" {
		return launchCwdArg
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	wd, _ := os.Getwd()
	return wd
}
