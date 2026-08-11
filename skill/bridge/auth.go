package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AuthManager owns pairing/token/rate-limit state — the auth "bootstrap"
// concerns that sit outside any single session.
type AuthManager struct {
	mu sync.Mutex

	sessionToken       string
	pairingCode        string
	pairingCodeExpires time.Time

	rateLimitAttempts    int
	rateLimitWindowStart time.Time

	bridgeState string // "idle" | "connected"
}

func newAuthManager() *AuthManager {
	return &AuthManager{
		bridgeState:          "idle",
		rateLimitWindowStart: time.Now(),
	}
}

func (a *AuthManager) GeneratePairingCode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.generatePairingCodeLocked()
}

func (a *AuthManager) generatePairingCodeLocked() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		// crypto/rand failure is not something we can meaningfully recover
		// from; fall back to a zeroed code rather than crashing pairing.
		n = big.NewInt(0)
	}
	code := fmt.Sprintf("%06d", n.Int64())
	a.pairingCode = code
	a.pairingCodeExpires = time.Now().Add(pairingCodeTTL)
	logMsg("info", fmt.Sprintf("Pairing code generated: %s (expires in 5 minutes)", code))
	return code
}

func generateSessionToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (a *AuthManager) isRateLimitedLocked() bool {
	now := time.Now()
	if now.Sub(a.rateLimitWindowStart) > rateLimitWindow {
		a.rateLimitAttempts = 0
		a.rateLimitWindowStart = now
	}
	return a.rateLimitAttempts >= rateLimitMaxTries
}

func (a *AuthManager) IsRateLimited() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isRateLimitedLocked()
}

func (a *AuthManager) RecordRateLimitAttempt() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if now.Sub(a.rateLimitWindowStart) > rateLimitWindow {
		a.rateLimitAttempts = 0
		a.rateLimitWindowStart = now
	}
	a.rateLimitAttempts++
}

// RequireAuth checks the request's bearer token against the current
// session token. Returns false if unauthenticated.
func (a *AuthManager) RequireAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionToken != "" && token == a.sessionToken
}

// Pair validates a pairing code and, on success, issues a new bearer
// token. Mirrors handlePair's core decision logic (sans HTTP plumbing).
type pairResult struct {
	ok    bool
	token string
	err   string
	code  int
}

func (a *AuthManager) Pair(code string) pairResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	if time.Now().After(a.pairingCodeExpires) {
		a.generatePairingCodeLocked()
		return pairResult{ok: false, code: http.StatusUnauthorized, err: "Pairing code expired. A new code has been generated."}
	}

	if code != a.pairingCode {
		return pairResult{ok: false, code: http.StatusUnauthorized, err: "Invalid pairing code"}
	}

	token := generateSessionToken()
	a.sessionToken = token
	a.pairingCode = ""
	a.pairingCodeExpires = time.Time{}
	a.bridgeState = "connected"

	return pairResult{ok: true, token: token}
}

func (a *AuthManager) BridgeState() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bridgeState
}
