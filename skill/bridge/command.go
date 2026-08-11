package main

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (br *Bridge) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}
	if !br.auth.RequireAuth(r) {
		jsonResponse(w, http.StatusUnauthorized, jmap{"error": "Unauthorized"})
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Invalid JSON"})
		return
	}

	sessionID, _ := strField(body, "sessionId")

	// --- Spawn a new session ---
	if spawnRequest, ok := strField(body, "spawn"); ok && spawnRequest != "" {
		if spawnRequest != "claude" {
			jsonResponse(w, http.StatusBadRequest, jmap{"error": fmt.Sprintf("Invalid agent: %s. Use: claude", spawnRequest)})
			return
		}
		cwd, _ := strField(body, "cwd")
		if cwd == "" {
			cwd = defaultCwd()
		}
		session, err := br.sessions.Spawn(spawnRequest, cwd)
		if err != nil {
			jsonResponse(w, http.StatusInternalServerError, jmap{"error": fmt.Sprintf("Failed to spawn %s", spawnRequest)})
			return
		}
		jsonResponse(w, http.StatusOK, jmap{"ok": true, "sessionId": session.ID, "agent": spawnRequest})
		return
	}

	// --- Kill a session ---
	if boolField(body, "kill") && sessionID != "" {
		if !br.sessions.Kill(sessionID) {
			jsonResponse(w, http.StatusNotFound, jmap{"error": "No session with that ID"})
			return
		}
		jsonResponse(w, http.StatusOK, jmap{"ok": true})
		return
	}

	// --- Permission response ---
	permissionID, hasPermissionID := strField(body, "permissionId")
	decision, hasDecision := body["decision"].(jmap)
	_, hasSelectedOption := body["selectedOption"]
	optionIndexIsInt := false
	if raw, ok := body["optionIndex"]; ok {
		if f, ok := raw.(float64); ok && f == math.Trunc(f) {
			optionIndexIsInt = true
		}
	}

	if hasPermissionID && permissionID != "" && (hasDecision || hasSelectedOption || optionIndexIsInt) {
		allowAll := boolField(body, "allowAll")
		if hasDecision {
			suggestions := br.permissions.PopSuggestions(permissionID)
			if behavior, _ := strField(decision, "behavior"); allowAll && behavior == "allow" {
				if suggestions == nil {
					suggestions = []any{}
				}
				decision["updatedPermissions"] = suggestions
			}
			if v, ok := body["selectedOption"]; ok {
				decision["selectedOption"] = v
			}
			if optionIndexIsInt {
				decision["optionIndex"] = body["optionIndex"]
			}

			if br.permissions.Resolve(permissionID, decision) {
				behavior, _ := strField(decision, "behavior")
				suffix := ""
				if allowAll {
					suffix = " (allow all)"
				}
				logMsg("info", fmt.Sprintf("Permission %s resolved: %s%s", permissionID, behavior, suffix))
				jsonResponse(w, http.StatusOK, jmap{"ok": true})
				return
			}
		}

		jsonResponse(w, http.StatusNotFound, jmap{"error": "No pending permission with that ID"})
		return
	}

	// --- PTY command injection ---
	if commandText, hasCommand := body["command"].(string); hasCommand {
		var targetSession *Session

		if sessionID != "" {
			s, ok := br.sessions.Get(sessionID)
			if !ok {
				jsonResponse(w, http.StatusNotFound, jmap{"error": "No session with that ID"})
				return
			}
			if !br.sessions.HasLivePty(sessionID) {
				br.handleHeadlessPrompt(w, sessionID, s, commandText)
				return
			}
			targetSession = s
		} else {
			targetSession = br.sessions.FindMostRecentActive()
			if targetSession == nil {
				targetSession = br.sessions.FindMostRecentRunning()
			}
		}

		if targetSession == nil {
			requestedAgent, _ := strField(body, "agent")
			if requestedAgent == "" {
				requestedAgent = "claude"
			}
			cwd, _ := strField(body, "cwd")
			if cwd == "" {
				cwd = defaultCwd()
			}
			session, err := br.sessions.Spawn(requestedAgent, cwd)
			if err != nil {
				jsonResponse(w, http.StatusInternalServerError, jmap{"error": fmt.Sprintf("Failed to spawn %s", requestedAgent)})
				return
			}
			newID := session.ID
			time.AfterFunc(autoSpawnWriteDelay, func() {
				if err := br.sessions.WriteStdin(newID, commandText); err == nil {
					logMsg("info", fmt.Sprintf("Command injected into new %s session %s (%d chars)", requestedAgent, newID, len(commandText)))
				}
			})
			jsonResponse(w, http.StatusOK, jmap{"ok": true, "sessionId": newID, "agent": requestedAgent, "spawned": true})
			return
		}

		if err := br.sessions.WriteStdin(targetSession.ID, commandText); err != nil {
			jsonResponse(w, http.StatusInternalServerError, jmap{"error": err.Error()})
			return
		}
		logMsg("info", fmt.Sprintf("Command injected into session %s (%d chars)", targetSession.ID, len(commandText)))
		jsonResponse(w, http.StatusOK, jmap{"ok": true, "sessionId": targetSession.ID, "agent": targetSession.Agent})
		return
	}

	jsonResponse(w, http.StatusBadRequest, jmap{"error": "Missing 'command', 'spawn', 'kill', or 'permissionId'+'decision'"})
}

// handleHeadlessPrompt runs `claude -p "<prompt>" --continue` for a session
// with no bridge-owned PTY (e.g. detected via a hook from a terminal Claude
// Code instance), streaming its output back as pty-output SSE events —
// the same client-visible event as a real PTY session.
func (br *Bridge) handleHeadlessPrompt(w http.ResponseWriter, sessionID string, s *Session, commandText string) {
	promptText := strings.TrimSpace(strings.TrimSuffix(commandText, "\n"))
	if promptText == "" {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Empty command"})
		return
	}
	if br.claudeBin == "" {
		jsonResponse(w, http.StatusInternalServerError, jmap{"error": fmt.Sprintf("No binary found for %s", s.Agent)})
		return
	}

	args := []string{"-p", promptText, "--continue"}
	logMsg("info", fmt.Sprintf("Running %s prompt in %s: %q", s.Agent, s.Cwd, truncateForLog(promptText, 80)))

	br.sessions.SetRunning(sessionID)

	proc, err := spawnHeadless(br.claudeBin, args, s.Cwd)
	if err != nil {
		logMsg("error", fmt.Sprintf("Prompt process error for session %s: %s", sessionID, err.Error()))
		jsonResponse(w, http.StatusInternalServerError, jmap{"error": err.Error()})
		return
	}

	go func() {
		exitCode, _, _ := proc.streamAndWait(func(chunk string, isStderr bool) {
			text := strings.TrimSpace(chunk)
			if text == "" {
				return
			}
			if isStderr && strings.Contains(text, "tcgetattr") {
				return
			}
			br.sse.PushEvent("pty-output", jmap{"text": text}, &sessionID)
		})
		logMsg("info", fmt.Sprintf("Prompt process exited (code %d) for session %s", exitCode, sessionID))
	}()

	jsonResponse(w, http.StatusOK, jmap{"ok": true, "sessionId": sessionID, "agent": s.Agent, "prompt": true})
}
