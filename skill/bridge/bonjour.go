package main

import (
	"fmt"
	"os"

	"github.com/grandcat/zeroconf"
)

// publishBonjour advertises the bridge as _claude-watch._tcp, mirroring
// server.js's bonjour-service publish() call field-for-field.
func publishBonjour(port int, bridgeID string) (*zeroconf.Server, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	txt := []string{
		"version=2",
		"bridgeId=" + bridgeID,
		"sessionId=" + bridgeID, // backward compat
		"machineName=" + hostname,
	}

	server, err := zeroconf.Register(
		fmt.Sprintf("Agent Watch Bridge (%s)", hostname),
		"_claude-watch._tcp",
		"local.",
		port,
		txt,
		nil,
	)
	if err != nil {
		return nil, err
	}

	logMsg("info", fmt.Sprintf("Bonjour advertising _claude-watch._tcp on port %d", port))
	return server, nil
}
