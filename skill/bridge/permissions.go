package main

import (
	"fmt"
	"sync"
	"time"
)

type pendingPermission struct {
	ch    chan jmap
	timer *time.Timer
}

// PermissionRegistry mirrors the pendingPermissions/pendingPermissionBodies
// maps and waitForPermission/resolvePermission functions: a blocking
// request/response handshake between a Claude Code hook call and the
// watch's decision, with a 10-minute auto-deny timeout.
type PermissionRegistry struct {
	mu      sync.Mutex
	pending map[string]*pendingPermission
	bodies  map[string][]any
}

func newPermissionRegistry() *PermissionRegistry {
	return &PermissionRegistry{
		pending: make(map[string]*pendingPermission),
		bodies:  make(map[string][]any),
	}
}

// Wait registers permissionID as pending and blocks until Resolve is
// called for it or the timeout fires (auto-deny).
func (p *PermissionRegistry) Wait(permissionID string) jmap {
	ch := make(chan jmap, 1)
	pp := &pendingPermission{ch: ch}
	pp.timer = time.AfterFunc(permissionTimeout, func() {
		p.mu.Lock()
		_, ok := p.pending[permissionID]
		if ok {
			delete(p.pending, permissionID)
		}
		p.mu.Unlock()
		if ok {
			logMsg("warn", fmt.Sprintf("Permission %s timed out after %ds, auto-denying", permissionID, int(permissionTimeout.Seconds())))
			ch <- jmap{"behavior": "deny", "reason": "Timed out waiting for watch response"}
		}
	})

	p.mu.Lock()
	p.pending[permissionID] = pp
	p.mu.Unlock()

	return <-ch
}

// Resolve delivers a decision to a pending Wait() call. Returns false if
// no such pending permission exists (already resolved/timed out/unknown).
func (p *PermissionRegistry) Resolve(permissionID string, decision jmap) bool {
	p.mu.Lock()
	pp, ok := p.pending[permissionID]
	if !ok {
		p.mu.Unlock()
		return false
	}
	delete(p.pending, permissionID)
	p.mu.Unlock()

	pp.timer.Stop()
	pp.ch <- decision
	return true
}

func (p *PermissionRegistry) StashSuggestions(permissionID string, suggestions []any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies[permissionID] = suggestions
}

// PopSuggestions returns and clears any stashed permission_suggestions
// for permissionID (used to populate updatedPermissions on allowAll).
func (p *PermissionRegistry) PopSuggestions(permissionID string) []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.bodies[permissionID]
	delete(p.bodies, permissionID)
	return s
}

func (p *PermissionRegistry) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// DenyAll resolves every pending permission as denied, used during
// graceful shutdown.
func (p *PermissionRegistry) DenyAll(reason string) {
	p.mu.Lock()
	pending := p.pending
	p.pending = make(map[string]*pendingPermission)
	p.mu.Unlock()

	for _, pp := range pending {
		pp.timer.Stop()
		pp.ch <- jmap{"behavior": "deny", "reason": reason}
	}
}
