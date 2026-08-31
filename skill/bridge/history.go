package main

import "net/http"

// maxHistoryMessages caps how much past chat handleHistory backfills for
// a client opening a session — recent context, not the whole transcript.
const maxHistoryMessages = 20

// handleHistory returns a session's recent chat history (past user/
// assistant text messages, oldest first), read from its Claude Code
// transcript file, so a client opening a session it has no live-
// accumulated lines for can still show what was said recently.
func (br *Bridge) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}
	if !br.auth.RequireAuth(r) {
		jsonResponse(w, http.StatusUnauthorized, jmap{"error": "Unauthorized"})
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "sessionId is required"})
		return
	}

	transcriptPath := br.sessions.GetTranscriptPath(sessionID)
	messages := RecentChatHistory(transcriptPath, maxHistoryMessages)

	jsonResponse(w, http.StatusOK, jmap{"messages": messages})
}
