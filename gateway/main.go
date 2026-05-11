package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

func main() {
	cfg, err := Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	setupLogger(cfg.LogLevel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	auth := NewAuthenticator(cfg)
	mgr, err := NewRedisSubscriber(cfg, nil)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer mgr.Close()

	hub := NewHub(cfg, mgr)
	mgr.SetDeliver(func(m incomingMessage) { hub.incoming <- m })
	mgr.Start()

	go hub.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", HealthzHandler(mgr))
	mux.HandleFunc("/ws/", wsHandler(cfg, hub, auth))

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		slog.Info("gateway listening", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
		}
	}()

	<-sigCh
	slog.Info("shutdown initiated")
	shuttingDown.Store(true)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	cancel()
}

// setupLogger installs a text logger at the configured level as the default
// slog logger.
func setupLogger(level slog.Level) {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

// wsHandler upgrades incoming /ws/ requests to WebSocket connections after
// validating the session with Django. The returned handler is safe for
// concurrent use; each call runs in its own goroutine spawned by net/http.
func wsHandler(cfg *Config, hub *Hub, auth *Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Origin check: only upgrade if the request's Origin is on the
		// allow-list. We delegate this to coder/websocket's option so the
		// upgrade rejection happens before we read any frames.
		origin := r.Header.Get("Origin")
		if origin != "" && !slices.Contains(cfg.AllowedOrigins, origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}

		// Extract the Django sessionid cookie. Without it we cannot ask
		// Django to authenticate the connection.
		sessionCookie, err := r.Cookie("sessionid")
		if err != nil || sessionCookie.Value == "" {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		result, err := auth.Validate(r.Context(), sessionCookie.Value)
		if err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: originPatternsFor(cfg.AllowedOrigins),
		})
		if err != nil {
			slog.Warn("websocket accept failed", "err", err)
			return
		}

		client := newPumpClient(hub, conn, result.UserID, result.SessionKey)
		for _, ch := range result.AllowedChannels {
			client.allowedChannels[ch] = true
		}
		hub.register <- client

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		go client.WritePump(ctx)
		client.ReadPump(ctx)
		_ = conn.CloseNow()
	}
}

// originPatternsFor converts ALLOWED_ORIGINS values into the host patterns
// coder/websocket expects. We strip the scheme so that wss:// and ws://
// requests targeting the same host are accepted uniformly.
func originPatternsFor(origins []string) []string {
	patterns := make([]string, 0, len(origins))
	for _, origin := range origins {
		host := origin
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		patterns = append(patterns, host)
	}
	return patterns
}
