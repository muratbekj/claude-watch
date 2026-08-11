package main

import "time"

const (
	portRangeStart = 7860
	portRangeEnd   = 7869

	pairingCodeTTL    = 5 * time.Minute
	rateLimitWindow   = 5 * time.Minute
	rateLimitMaxTries = 5

	sseHeartbeatInterval = 10 * time.Second
	sseBufferSize        = 500

	permissionTimeout = 10 * time.Minute

	autoSpawnWriteDelay = 500 * time.Millisecond
	shutdownForceExit   = 5 * time.Second
)
