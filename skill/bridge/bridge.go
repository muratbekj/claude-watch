package main

// Bridge aggregates the server's components. Handlers are methods on
// *Bridge so they can reach whichever pieces of state they need without
// a pile of global variables (the concurrency-safety tradeoff Node didn't
// have to make, called out in the design doc).
type Bridge struct {
	id          string
	auth        *AuthManager
	sessions    *SessionRegistry
	sse         *SSEHub
	permissions *PermissionRegistry
	claudeBin   string
	shutdownCh  chan struct{}
}

func availableAgents(claudeBin string) []string {
	if claudeBin != "" {
		return []string{"claude"}
	}
	return []string{}
}
