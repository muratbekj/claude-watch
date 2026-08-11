package main

import "net/http"

func (br *Bridge) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResponse(w, http.StatusMethodNotAllowed, jmap{"error": "Method not allowed"})
		return
	}

	if br.auth.IsRateLimited() {
		jsonResponse(w, http.StatusTooManyRequests, jmap{"error": "Too many pairing attempts. Try again later."})
		return
	}

	body, err := readBody(r)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Invalid JSON"})
		return
	}

	br.auth.RecordRateLimitAttempt()

	code, ok := strField(body, "code")
	if !ok || code == "" {
		jsonResponse(w, http.StatusBadRequest, jmap{"error": "Missing 'code' field"})
		return
	}

	result := br.auth.Pair(code)
	if !result.ok {
		jsonResponse(w, result.code, jmap{"error": result.err})
		return
	}

	br.sse.PushEvent("session", jmap{"state": "connected"}, nil)
	logMsg("info", "Watch paired successfully")

	jsonResponse(w, http.StatusOK, jmap{
		"token":           result.token,
		"bridgeId":        br.id,
		"sessionId":       br.id, // backward compat
		"availableAgents": availableAgents(br.claudeBin),
		"sessions":        br.sessions.Snapshot(),
	})
}
