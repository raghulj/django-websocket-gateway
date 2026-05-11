package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// shuttingDown is a process-wide flag flipped by main() once a shutdown
// signal arrives. /healthz returns 503 while it is set so load balancers
// drain traffic before the process exits.
var shuttingDown atomic.Bool

// HealthzHandler returns an HTTP handler for /healthz that reports the
// liveness of the gateway.
//
// The endpoint serves as both liveness and readiness probe. It returns:
//
//   - 503 "shutting down" while shuttingDown is set.
//   - 503 "redis unreachable" when a 1-second Redis PING fails.
//   - 200 "ok" otherwise.
//
// The combined probe is intentional: there is no /readyz endpoint per the
// project scope, and 503-on-shutdown lets Kubernetes-style probes redirect
// traffic without an explicit drain command.
func HealthzHandler(redis *RedisSubscriber) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if shuttingDown.Load() {
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		defer cancel()
		if err := redis.Ping(ctx); err != nil {
			http.Error(w, "redis unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}
