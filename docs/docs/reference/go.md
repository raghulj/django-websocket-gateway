# Go gateway API reference

Generated from godoc comments by `docs/scripts/render-godoc.sh`. **Do
not edit this file by hand** — change the comments in `gateway/*.go` and
re-run the script.

```
Package main implements the django-websocket-gateway WebSocket gateway.

The gateway runs as a standalone process alongside Django. It accepts browser
WebSocket connections, validates each one with Django via the /internal/ws-auth/
endpoint, and fans out messages from a Redis pub/sub backplane to subscribed
clients.

CONSTANTS

const (
	// CloseSessionRevoked closes a connection whose Django session has been
	// invalidated (logout, force-logout, ban).
	CloseSessionRevoked = 4401
	// CloseSlowClient closes a connection that fails to drain its send buffer.
	CloseSlowClient = 4408
	// CloseTooManyConnections closes a connection refused because a
	// per-user or per-process cap is exceeded.
	CloseTooManyConnections = 4429
)
    WebSocket application-defined close codes used by the gateway.


VARIABLES

var (
	// ErrUnauthenticated indicates Django rejected the session (HTTP 401 or
	// {"authenticated": false}). Connections should be closed with code 4401.
	ErrUnauthenticated = errors.New("websocket-auth: unauthenticated")
	// ErrAuthFailed wraps any other failure mode (transport error, 5xx,
	// malformed JSON, timeout). The connection should be refused with a
	// non-permanent close code; the client may retry.
	ErrAuthFailed = errors.New("websocket-auth: failed")
)
    Sentinel errors. Use errors.Is to distinguish them at call sites.

var ErrShortSecret = errors.New("INTERNAL_AUTH_SECRET must be at least 32 characters")
    ErrShortSecret is returned by Load when INTERNAL_AUTH_SECRET is shorter than
    the minimum length. The wrapped error message intentionally does not echo
    the secret value (hard rule #3).


FUNCTIONS

func HealthzHandler(redis *RedisSubscriber) http.HandlerFunc
    HealthzHandler returns an HTTP handler for /healthz that reports the
    liveness of the gateway.

    The endpoint serves as both liveness and readiness probe. It returns:

      - 503 "shutting down" while shuttingDown is set.
      - 503 "redis unreachable" when a 1-second Redis PING fails.
      - 200 "ok" otherwise.

    The combined probe is intentional: there is no /readyz endpoint per the
    project scope, and 503-on-shutdown lets Kubernetes-style probes redirect
    traffic without an explicit drain command.


TYPES

type AuthResult struct {
	UserID          int64    `json:"user_id"`
	Username        string   `json:"username"`
	AllowedChannels []string `json:"allowed_channels"`
	// SessionKey is the session ID supplied to Validate, NOT a value returned
	// by Django. The hub uses it to address the per-session control channel.
	SessionKey string `json:"-"`
}
    AuthResult is the validated outcome of a Django auth call.

    The struct combines fields returned by Django (UserID, Username,
    AllowedChannels) with the SessionKey supplied as input. SessionKey is
    echoed back into the result because the hub needs it at register time to
    auto-subscribe the client to its _session:{key} control channel.

type Authenticator struct {
	// Has unexported fields.
}
    Authenticator is a stateless client for Django's /internal/ws-auth/.

    One Authenticator is constructed at startup with NewAuthenticator and shared
    across all goroutines; the underlying http.Client is safe for concurrent
    use.

func NewAuthenticator(cfg *Config) *Authenticator
    NewAuthenticator builds an Authenticator from cfg.

    The HTTP client uses cfg.AuthTimeout as the per-request deadline.
    Connections are reused via the default transport's connection pool.

func (a *Authenticator) Validate(ctx context.Context, sessionID string) (*AuthResult, error)
    Validate asks Django whether sessionID is currently valid and, if so,
    returns the user identity and list of allowed channels.

    The shared secret is sent in the X-Internal-Auth header. The session ID is
    sent in X-Forwarded-Session so it never appears as a cookie or in the URL
    (and never in any log emitted by this package — only a short SHA-256 digest
    of the session ID is logged when something goes wrong).

    Returns ErrUnauthenticated when Django says the session is invalid, and
    ErrAuthFailed when the call itself fails (transport, 5xx, malformed JSON).
    Both errors are sentinel-wrapped; use errors.Is to distinguish.

type Client struct {
	// Has unexported fields.
}
    Client is the hub-side view of a connected WebSocket. It is mutated only
    by the hub goroutine after the client is registered. WritePump owns the
    connection-side state (the actual *websocket.Conn) and is responsible for
    turning `send` payloads into frames and `disconnect` signals into close
    frames.

func (c *Client) ReadPump(ctx context.Context)
    ReadPump consumes inbound frames from the WebSocket. Each frame is parsed as
    a clientFrame; valid subscribe/unsubscribe actions are forwarded to the hub.
    Anything else is silently dropped (we never echo input).

    ReadPump owns the read side of conn. It exits — and closes the connection —
    when:

      - The supplied context is cancelled.
      - conn returns any read error (network error, oversized frame, peer
        close).
      - A peer sends a frame larger than cfg.MaxMessageSize (enforced via
        conn.SetReadLimit).

    The deferred close triggers WritePump to exit too (both pumps share conn).

func (c *Client) WritePump(ctx context.Context)
    WritePump fans hub-side payloads and disconnect signals out to the
    WebSocket. It runs until any of:

      - The supplied context is cancelled.
      - cfg.ConnectionMaxLifetime elapses (a clean close with code 1000).
      - A disconnect signal arrives (close with the supplied code/reason).
      - The send channel is closed by the hub.
      - A write error occurs.

    The write side of conn is owned by this goroutine; ReadPump must not call
    conn.Write itself.

type Config struct {
	// ListenAddr is the TCP address the HTTP server binds to, e.g. ":8080".
	ListenAddr string
	// RedisURL is the redis://... URL of the pub/sub backplane.
	RedisURL string
	// DjangoAuthURL is the absolute URL of Django's /internal/ws-auth/ endpoint.
	DjangoAuthURL string
	// InternalAuthSecret is the shared secret sent in the X-Internal-Auth
	// header on every call to Django. Never log this value.
	InternalAuthSecret string
	// AllowedOrigins is the list of Origin header values accepted on the
	// WebSocket upgrade. Empty means "no upgrade is permitted".
	AllowedOrigins []string
	// MaxConnectionsPerUser caps simultaneous WebSocket connections from one user.
	MaxConnectionsPerUser int
	// MaxConnectionsTotal caps simultaneous WebSocket connections process-wide.
	MaxConnectionsTotal int
	// MaxMessageSize is the maximum allowed size, in bytes, of an inbound frame.
	MaxMessageSize int64
	// ConnectionMaxLifetime is the upper bound on the lifetime of a single
	// WebSocket connection. The server closes it cleanly when reached.
	ConnectionMaxLifetime time.Duration
	// PingInterval is how often the server sends a WebSocket ping.
	PingInterval time.Duration
	// PongTimeout is the read deadline used to detect dead connections.
	PongTimeout time.Duration
	// LogLevel sets the slog log level. "debug", "info", "warn", "error".
	LogLevel slog.Level
	// AuthTimeout caps each Django auth call. Default 5s.
	AuthTimeout time.Duration
}
    Config holds every runtime parameter the gateway needs.

    All fields are populated from environment variables by Load. The struct
    is immutable after construction — pass it by pointer to avoid copies,
    never mutate it.

func Load() (*Config, error)
    Load reads the gateway configuration from the process environment.

    Required environment variables:

        REDIS_URL              - pub/sub backplane URL
        DJANGO_AUTH_URL        - absolute URL of /internal/ws-auth/
        INTERNAL_AUTH_SECRET   - shared secret, ≥ 32 characters
        ALLOWED_ORIGINS        - comma-separated list of allowed Origin values

    Optional environment variables (with defaults):

        LISTEN_ADDR              :8080
        MAX_CONNECTIONS_PER_USER 10
        MAX_CONNECTIONS_TOTAL    50000
        MAX_MESSAGE_SIZE         8192
        CONNECTION_MAX_LIFETIME  12h
        PING_INTERVAL            30s
        PONG_TIMEOUT             60s
        LOG_LEVEL                info

    Load returns an error that names the offending variable when validation
    fails. The error never contains the value of INTERNAL_AUTH_SECRET.

type Hub struct {
	// Has unexported fields.
}
    Hub is the broker that routes pub/sub messages between Redis and connected
    WebSocket clients.

    All shared state (clients, channels, userClients) is owned by the goroutine
    running Hub.Run. External callers communicate exclusively through the
    channel fields.

func NewHub(cfg *Config, manager channelManager) *Hub
    NewHub constructs a Hub bound to cfg and the supplied channelManager.

    The hub's goroutine is not started until Run is called. manager may be nil;
    in that case subscribe/unsubscribe operations are local-only (useful for
    tests of the routing logic itself, but unwanted in production).

func (h *Hub) Run(ctx context.Context)
    Run executes the hub loop until ctx is cancelled.

    The loop owns every map on the Hub. No other goroutine reads or writes them;
    cross-goroutine communication is via the channel fields exclusively.
    On ctx.Done, the loop exits cleanly without draining (the caller is expected
    to have already initiated a graceful shutdown via WritePumps).

type RedisSubscriber struct {
	// Has unexported fields.
}
    RedisSubscriber owns the single Redis pub/sub connection.

    The subscriber refcounts logical subscribers per channel: callers may
    invoke Subscribe and Unsubscribe many times, but the underlying Redis
    SUBSCRIBE happens only on the 0→1 transition and UNSUBSCRIBE only on the 1→0
    transition. This keeps the pub/sub connection small and predictable.

    All public methods are safe for concurrent use.

func NewRedisSubscriber(cfg *Config, deliver incomingHandler) (*RedisSubscriber, error)
    NewRedisSubscriber dials Redis using cfg.RedisURL and prepares a pub/sub
    session. Dialing is lazy in go-redis; the first command (Ping, Subscribe)
    surfaces connection errors.

    The optional deliver callback may be supplied here or attached later via
    SetDeliver. The subscriber does not start reading until Start is called
    — letting the caller resolve construction-order cycles (e.g., the hub
    references the subscriber and vice-versa).

func (r *RedisSubscriber) Close() error
    Close terminates the pub/sub session and closes the underlying connection.
    Idempotent.

func (r *RedisSubscriber) Ping(ctx context.Context) error
    Ping verifies the Redis connection is alive. Used by /healthz.

func (r *RedisSubscriber) SetDeliver(deliver incomingHandler)
    SetDeliver attaches (or replaces) the deliver callback. Safe to call before
    Start; if called after, the new callback applies to subsequent messages.

func (r *RedisSubscriber) Start()
    Start spawns the read-loop goroutine. Subsequent calls are no-ops.

func (r *RedisSubscriber) Subscribe(channel string) error
    Subscribe increments the refcount for channel. The first subscriber on a
    channel triggers a Redis SUBSCRIBE; subsequent ones are local-only.

func (r *RedisSubscriber) Unsubscribe(channel string) error
    Unsubscribe decrements the refcount for channel. The last unsubscribe
    triggers a Redis UNSUBSCRIBE. Unknown channels are a no-op.

```
