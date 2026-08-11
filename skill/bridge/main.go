package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/grandcat/zeroconf"
)

func buildMux(br *Bridge) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/pair", br.handlePair)
	mux.HandleFunc("/command", br.handleCommand)
	mux.HandleFunc("/events", br.handleEvents)
	mux.HandleFunc("/hooks/tool-output", br.handleHookToolOutput)
	mux.HandleFunc("/hooks/permission", br.handleHookPermission)
	mux.HandleFunc("/hooks/stop", br.handleHookStop)
	mux.HandleFunc("/hooks/task-complete", br.handleHookTaskComplete)
	mux.HandleFunc("/hooks/error", br.handleHookError)
	mux.HandleFunc("/status", br.handleStatus)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusNotFound, jmap{"error": "Not found"})
	})
	return mux
}

// recoverMiddleware mirrors onRequest's try/catch around each handler:
// an unhandled panic becomes a 500 instead of taking down the process.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logMsg("error", fmt.Sprintf("Unhandled error in %s %s: %v", r.Method, r.URL.Path, rec))
				jsonResponse(w, http.StatusInternalServerError, jmap{"error": "Internal server error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func bindListener() (net.Listener, int, error) {
	for port := portRangeStart; port <= portRangeEnd; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			return ln, port, nil
		}
		if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") {
			logMsg("warn", fmt.Sprintf("Port %d in use, trying next...", port))
			continue
		}
		return nil, 0, err
	}
	return nil, 0, fmt.Errorf("no available port in range %d-%d", portRangeStart, portRangeEnd)
}

func lanIP() string {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range ifaces {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 != nil {
			return ip4.String()
		}
	}
	return "127.0.0.1"
}

func printBanner(code string, ip string, port int, agents []string) {
	agentLine := "none"
	if len(agents) > 0 {
		agentLine = strings.Join(agents, " + ")
	}
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════╗")
	fmt.Println("║        AGENT WATCH BRIDGE             ║")
	fmt.Println("╠═══════════════════════════════════════╣")
	fmt.Printf("║  Pairing Code:  %s                ║\n", code)
	fmt.Printf("║  IP Address:    %-20s║\n", ip)
	fmt.Printf("║  Port:          %-20s║\n", fmt.Sprintf("%d", port))
	fmt.Printf("║  Agents:        %-20s║\n", agentLine)
	fmt.Println("╚═══════════════════════════════════════╝")
	fmt.Println()
}

func main() {
	if len(os.Args) > 1 {
		launchCwdArg = os.Args[1]
	}

	claudeBin := findClaudeBinary()

	listener, port, err := bindListener()
	if err != nil {
		logMsg("error", err.Error())
		os.Exit(1)
	}
	logMsg("info", fmt.Sprintf("Bridge server listening on 0.0.0.0:%d", port))

	sse := newSSEHub()
	br := &Bridge{
		id:          newUUID(),
		auth:        newAuthManager(),
		sessions:    newSessionRegistry(sse, claudeBin),
		sse:         sse,
		permissions: newPermissionRegistry(),
		claudeBin:   claudeBin,
		shutdownCh:  make(chan struct{}),
	}

	code := br.auth.GeneratePairingCode()

	var bonjourServer *zeroconf.Server
	if s, err := publishBonjour(port, br.id); err != nil {
		logMsg("warn", fmt.Sprintf("Bonjour publish failed: %s", err.Error()))
	} else {
		bonjourServer = s
	}

	agents := availableAgents(claudeBin)
	agentLabels := make([]string, 0, len(agents))
	for _, a := range agents {
		agentLabels = append(agentLabels, strings.ToUpper(a[:1])+a[1:])
	}
	agentSummary := "none"
	if len(agentLabels) > 0 {
		agentSummary = strings.Join(agentLabels, ", ")
	}
	logMsg("info", fmt.Sprintf("Bridge ready. Available agents: %s. Sessions spawn on demand.", agentSummary))

	printBanner(code, lanIP(), port, agentLabels)

	httpServer := &http.Server{
		Handler: recoverMiddleware(buildMux(br)),
		// No ReadTimeout/WriteTimeout: /hooks/permission blocks up to
		// permissionTimeout (10 min) waiting for a watch response.
	}

	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- httpServer.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var shutdownOnce sync.Once
	shutdown := func(reason string) {
		shutdownOnce.Do(func() {
			logMsg("info", fmt.Sprintf("Received %s, shutting down gracefully...", reason))

			close(br.shutdownCh)
			br.sessions.KillAll()

			if bonjourServer != nil {
				bonjourServer.Shutdown()
			}

			br.permissions.DenyAll("Server shutting down")

			ctx, cancel := context.WithTimeout(context.Background(), shutdownForceExit)
			defer cancel()
			if err := httpServer.Shutdown(ctx); err != nil {
				logMsg("warn", "Forced exit after timeout")
				os.Exit(1)
			}
			logMsg("info", "Server closed")
			os.Exit(0)
		})
	}

	select {
	case sig := <-sigCh:
		shutdown(sig.String())
	case err := <-serveErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logMsg("error", fmt.Sprintf("Failed to start server: %s", err.Error()))
			os.Exit(1)
		}
	}
}
