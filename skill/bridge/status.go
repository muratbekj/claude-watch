package main

import "net/http"

func (br *Bridge) handleStatus(w http.ResponseWriter, r *http.Request) {
	var activeAgent any
	mostRecentRunning := br.sessions.FindMostRecentRunning()
	if mostRecentRunning != nil {
		activeAgent = mostRecentRunning.Agent
	}

	jsonResponse(w, http.StatusOK, jmap{
		"bridgeId":           br.id,
		"sessionId":          br.id, // backward compat
		"state":              br.auth.BridgeState(),
		"availableAgents":    availableAgents(br.claudeBin),
		"sessions":           br.sessions.Snapshot(),
		"sseClients":         br.sse.clientCount(),
		"pendingPermissions": br.permissions.Count(),
		"eventBufferSize":    br.sse.bufferSize(),
		"hasPty":             br.sessions.FindMostRecentActive() != nil,
		"activeAgent":        activeAgent,
	})
}
