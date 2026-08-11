package main

import (
	"fmt"
	"net/http"
)

// --- Hook handlers ---
// Hooks come from Claude Code's own hook runner (setup-hooks.sh registers
// these URLs in ~/.claude/settings.json). None of them require auth —
// they're invoked by a local subprocess on localhost, same as server.js.

func (br *Bridge) handleHookToolOutput(w http.ResponseWriter, r *http.Request) {
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
	source, ok := strField(body, "source")
	if !ok || source == "" {
		source = "claude"
	}
	toolName, _ := strField(body, "tool_name")
	label := "PostToolUse"
	if source == "codex" {
		label = "Codex"
	}
	logMsg("info", fmt.Sprintf("Hook: %s received [%s] session=%s", label, source, sid), toolName)

	payload := mergeMaps(body, jmap{"source": source})
	br.sse.PushEvent("tool-output", payload, &sid)
	jsonResponse(w, http.StatusOK, jmap{"ok": true})
}

func (br *Bridge) handleHookPermission(w http.ResponseWriter, r *http.Request) {
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
	permissionID := newUUID()
	toolName, _ := strField(body, "tool_name")
	logMsg("info", fmt.Sprintf("Hook: PermissionRequest received (id: %s) session=%s", permissionID, sid), toolName)

	if suggestions, ok := body["permission_suggestions"].([]any); ok {
		br.permissions.StashSuggestions(permissionID, suggestions)
	}

	// Matches server.js's `{ permissionId, ...body }` spread: body's own
	// fields are overlaid on top and can shadow permissionId if present.
	payload := mergeMaps(jmap{"permissionId": permissionID}, body)
	br.sse.PushEvent("permission-request", payload, &sid)

	decision := br.permissions.Wait(permissionID)

	behavior, _ := strField(decision, "behavior")
	logMsg("info", fmt.Sprintf("Hook: PermissionRequest resolved (id: %s): %s", permissionID, behavior))

	decisionOut := jmap{"behavior": behavior}

	if updated, ok := decision["updatedPermissions"].([]any); ok && len(updated) > 0 {
		decisionOut["updatedPermissions"] = updated
	}

	if behavior == "deny" {
		if msg, ok := strField(decision, "message"); ok && msg != "" {
			decisionOut["message"] = msg
		}
	}

	// AskUserQuestion: forward the watch-selected option as the answer so
	// Claude Code doesn't fall back to waiting for terminal input.
	if selectedOption, hasSelected := decision["selectedOption"]; hasSelected {
		if toolName == "AskUserQuestion" {
			if toolInput, ok := body["tool_input"].(jmap); ok {
				if questions, ok := toolInput["questions"].([]any); ok && len(questions) > 0 {
					if q0, ok := questions[0].(jmap); ok {
						if questionText, ok := strField(q0, "question"); ok && questionText != "" {
							answers := jmap{questionText: selectedOption}
							decisionOut["updatedInput"] = jmap{"questions": questions, "answers": answers}
							logMsg("info", fmt.Sprintf("AskUserQuestion answer forwarded: %q", fmt.Sprint(selectedOption)))
						}
					}
				}
			}
		}
	}

	jsonResponse(w, http.StatusOK, jmap{
		"hookSpecificOutput": jmap{
			"hookEventName": "PermissionRequest",
			"decision":      decisionOut,
		},
	})
}

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

func (br *Bridge) handleHookTaskComplete(w http.ResponseWriter, r *http.Request) {
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
	logMsg("info", fmt.Sprintf("Hook: TaskCompleted received session=%s", sid))
	br.sse.PushEvent("task-complete", body, &sid)
	jsonResponse(w, http.StatusOK, jmap{"ok": true})
}

func (br *Bridge) handleHookError(w http.ResponseWriter, r *http.Request) {
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
	errText, _ := strField(body, "error")
	logMsg("info", fmt.Sprintf("Hook: Error received session=%s", sid), errText)
	br.sse.PushEvent("error", body, &sid)
	jsonResponse(w, http.StatusOK, jmap{"ok": true})
}
