# Threat model

What v1 covers, what it doesn't, and when you should upgrade or layer
additional controls.

## In scope

| Threat | Control |
|---|---|
| Unauthorised access to `/internal/ws-auth/` | Reverse proxy returns 404 for `/internal/*`. Django view enforces shared secret in `X-Internal-Auth` regardless. |
| Brute-force on the shared secret | 32-character minimum; timing-safe comparison via `hmac.compare_digest` / `crypto/subtle.ConstantTimeCompare`. |
| Cross-user channel access | The gateway's hub rejects any subscribe to a channel outside the user's `allowed_channels`. The list is computed server-side at handshake. |
| Subscription to internal control channels | Client-initiated subscribes to `_`-prefixed channels are rejected. |
| Replay of session ID via the auth endpoint | Session ID is sent in `X-Forwarded-Session` rather than as a cookie or in the URL. The endpoint is unreachable publicly. |
| Secret leak via logs | Secret never appears in logs, exceptions, or response bodies. Tests assert this on every path. Pre-commit hook scans for new leaks. |
| Malicious or corrupt binary download | SHA-256 verified before execute bits are set. Mismatch is a hard error with no file left at the destination. |
| Slow / hung clients filling memory | Each client has a bounded send buffer. The hub disconnects on a full buffer (close code 4408). |
| Excessive connection counts | Per-user (`MAX_CONNECTIONS_PER_USER`) and process-wide (`MAX_CONNECTIONS_TOTAL`) caps. Excess close with 4429. |
| Oversized inbound frames | `MaxMessageSize` enforced by the WebSocket library; oversized → connection close. |
| Session staying live after logout | `user_logged_out` signal publishes a revoke message on the per-session control channel; the gateway closes the WebSocket with 4401. |
| Coordinated bans / "logout everywhere" | `force_logout_user(user)` publishes on the per-user revoke channel. |

## Out of scope (v1)

| Threat | Mitigation outside this package |
|---|---|
| Origin spoofing for non-browser clients | The `ALLOWED_ORIGINS` check defends against malicious websites, not against direct WebSocket clients. Combine with mutual-TLS or a separate API key if you publish to channels from non-browser sources. |
| DDoS at the TCP layer | Front the gateway with Cloudflare, AWS Shield, or your platform's DDoS protection. The gateway has no upstream rate limiting. |
| Per-message authorisation | Channels are the authorization unit. A user can see every message published to a channel they are subscribed to. Don't put information in a channel that some subscribers shouldn't see. |
| Compromise of the publisher | A compromised Django process can publish anything to any channel. Treat `publish()` as you would the database write API. |
| Message persistence | A disconnected client misses messages published in its absence. Store anything critical in your database and use `publish()` only as a real-time prompt. |
| Replay on reconnect | Not supported in v1. |
| Presence (who's online?) | Not supported in v1. Derive from your own data if needed. |
| Secret rotation without downtime | v1 requires restarting both processes. For zero-downtime rotation, run two gateway pools with overlapping configs during the cutover. |
| Compromise of GitHub Releases | SHA-256 verification protects against a tampered binary on the release, but not against a compromise of the project's own signing keys. Mirror the binary internally and use `WS_GATEWAY_BINARY_PATH` if your threat model demands it. |

## When you should upgrade your controls

- **More than one Django app shares the gateway.** Give each its own
  `INTERNAL_SECRET` and run a gateway pool per app — the gateway is
  not multi-tenant in v1.
- **Public channels.** v1 assumes every subscription is authenticated.
  If you need anonymous-readable channels, you'll need to extend
  `ws_auth` to accept anonymous sessions and adjust the callback.
- **Compliance / audit trail.** v1 logs failures with structured
  fields but does not produce an audit log of publishes or
  subscriptions. Wrap `publish()` with an audit decorator on your
  side.

## Hard rules enforced by the code and the hooks

1. The shared secret is dedicated and ≥ 32 chars; never equals
   `SECRET_KEY`.
2. The secret never appears in logs, exceptions, or HTTP responses.
3. Secret comparison is timing-safe.
4. The gateway never decides what channels a user may see — Django
   does.
5. The gateway never reads Django's database.
6. Hub state mutates only inside the hub goroutine. Cross-goroutine
   communication is via channels.
7. Downloaded binaries are SHA-256 verified before execution bits are
   set.
8. Client subscriptions to `_`-prefixed channels are rejected.
