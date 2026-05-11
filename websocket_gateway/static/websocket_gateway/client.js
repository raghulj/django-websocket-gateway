/**
 * Browser WebSocket client for django-websocket-gateway.
 *
 * Reconnects with exponential backoff (1s → 2s → 4s → … capped at 30s) on
 * abnormal close. Stops reconnecting after a 4401 close, which the gateway
 * sends when Django has revoked the session.
 *
 * @example
 *   import { WSClient } from "/static/websocket_gateway/client.js";
 *   const ws = new WSClient(`wss://${location.host}/ws/`);
 *   ws.on("user-42", (payload) => console.log("notification:", payload));
 *   ws.subscribe("user-42");
 */
export class WSClient {
  /**
   * @param {string} url - WebSocket URL, e.g. "wss://example.com/ws/".
   * @param {{channels?: string[]}} [options]
   */
  constructor(url, options = {}) {
    this.url = url;
    this.channels = new Set(options.channels || []);
    this.handlers = new Map();
    this.backoff = 1000;
    this.maxBackoff = 30000;
    this.shouldReconnect = true;
    this._connect();
  }

  /**
   * Register a handler for a channel. Replaces any prior handler.
   * @param {string} channel
   * @param {(payload: unknown) => void} handler
   */
  on(channel, handler) {
    this.handlers.set(channel, handler);
  }

  /**
   * Add a channel to the subscription set and (if connected) send the
   * subscribe frame. New connections re-send all subscriptions on open.
   * @param {string} channel
   */
  subscribe(channel) {
    this.channels.add(channel);
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ action: "subscribe", channel }));
    }
  }

  /**
   * Remove a channel and (if connected) send the unsubscribe frame.
   * @param {string} channel
   */
  unsubscribe(channel) {
    this.channels.delete(channel);
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ action: "unsubscribe", channel }));
    }
  }

  /**
   * Close the connection and disable auto-reconnect.
   */
  close() {
    this.shouldReconnect = false;
    if (this.ws) {
      this.ws.close();
    }
  }

  _connect() {
    this.ws = new WebSocket(this.url);

    this.ws.onopen = () => {
      this.backoff = 1000;
      for (const ch of this.channels) {
        this.ws.send(JSON.stringify({ action: "subscribe", channel: ch }));
      }
    };

    this.ws.onmessage = (event) => {
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch {
        return;
      }
      if (msg.type === "error") {
        console.warn("WS error:", msg);
        return;
      }
      const handler = this.handlers.get(msg.channel);
      if (handler) {
        handler(msg.payload);
      }
    };

    this.ws.onclose = (event) => {
      if (!this.shouldReconnect) return;
      // 4401 = unauthorized (revoked or session invalid); stop trying.
      if (event.code === 4401) {
        console.warn("WS unauthorized; not reconnecting");
        return;
      }
      const delay = this.backoff + Math.random() * 1000;
      setTimeout(() => this._connect(), delay);
      this.backoff = Math.min(this.backoff * 2, this.maxBackoff);
    };

    this.ws.onerror = () => {
      if (this.ws) this.ws.close();
    };
  }
}
