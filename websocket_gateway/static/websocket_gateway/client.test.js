/**
 * Tests for the browser WebSocket client.
 *
 * Run with: `node --test websocket_gateway/static/websocket_gateway/client.test.js`
 *
 * Uses a minimal in-process WebSocket double — sufficient to exercise the
 * client's state machine (subscribe-on-open, error handling, reconnect-on-
 * close, no-reconnect-on-4401). No DOM is required.
 */

import { test } from "node:test";
import assert from "node:assert/strict";
import { WSClient } from "./client.js";

class FakeWebSocket {
  static OPEN = 1;
  static CLOSED = 3;
  static instances = [];

  constructor(url) {
    this.url = url;
    this.readyState = 0; // CONNECTING
    this.sent = [];
    this.onopen = null;
    this.onmessage = null;
    this.onclose = null;
    this.onerror = null;
    FakeWebSocket.instances.push(this);
  }

  send(data) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
  }

  // Test helpers
  open() {
    this.readyState = FakeWebSocket.OPEN;
    if (this.onopen) this.onopen({});
  }
  message(data) {
    if (this.onmessage) this.onmessage({ data });
  }
  serverClose(code = 1006) {
    this.readyState = FakeWebSocket.CLOSED;
    if (this.onclose) this.onclose({ code });
  }
}

function withFakeWebSocket(fn) {
  const originalWS = globalThis.WebSocket;
  const originalSetTimeout = globalThis.setTimeout;
  const scheduled = [];
  globalThis.WebSocket = FakeWebSocket;
  globalThis.setTimeout = (cb) => {
    scheduled.push(cb);
    return 0;
  };
  FakeWebSocket.instances = [];
  try {
    fn({
      lastWs: () => FakeWebSocket.instances[FakeWebSocket.instances.length - 1],
      allWs: () => FakeWebSocket.instances,
      flushTimers: () => {
        const pending = scheduled.splice(0);
        pending.forEach((cb) => cb());
      },
    });
  } finally {
    globalThis.WebSocket = originalWS;
    globalThis.setTimeout = originalSetTimeout;
  }
}

test("subscribe after open sends one subscribe frame", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    client.subscribe("user-42");
    assert.deepEqual(JSON.parse(lastWs().sent[0]), {
      action: "subscribe",
      channel: "user-42",
    });
  });
});

test("subscribe before open is replayed on open", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x", { channels: ["user-1"] });
    assert.equal(lastWs().sent.length, 0);
    lastWs().open();
    assert.equal(lastWs().sent.length, 1);
    assert.deepEqual(JSON.parse(lastWs().sent[0]), {
      action: "subscribe",
      channel: "user-1",
    });
    void client;
  });
});

test("unsubscribe after open sends one unsubscribe frame", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    client.subscribe("ch");
    client.unsubscribe("ch");
    assert.deepEqual(JSON.parse(lastWs().sent[1]), {
      action: "unsubscribe",
      channel: "ch",
    });
  });
});

test("handler invoked on matching channel message", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    let received = null;
    client.on("ch", (p) => {
      received = p;
    });
    lastWs().message(JSON.stringify({ channel: "ch", payload: { a: 1 } }));
    assert.deepEqual(received, { a: 1 });
  });
});

test("error frames do not invoke handlers", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    let calls = 0;
    client.on("ch", () => {
      calls++;
    });
    lastWs().message(JSON.stringify({ type: "error", channel: "ch", reason: "forbidden" }));
    assert.equal(calls, 0);
  });
});

test("malformed JSON is silently ignored", () => {
  withFakeWebSocket(({ lastWs }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    client.on("ch", () => {
      throw new Error("should not be called");
    });
    lastWs().message("not json");
    // No throw = pass.
    void client;
  });
});

test("close code 4401 stops reconnection", () => {
  withFakeWebSocket(({ lastWs, allWs, flushTimers }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    lastWs().serverClose(4401);
    flushTimers();
    assert.equal(allWs().length, 1, "no new WebSocket created");
    void client;
  });
});

test("close code 1006 triggers reconnect with backoff", () => {
  withFakeWebSocket(({ lastWs, allWs, flushTimers }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    lastWs().serverClose(1006);
    flushTimers();
    assert.equal(allWs().length, 2, "second WebSocket created on reconnect");
    void client;
  });
});

test("explicit close() prevents reconnection", () => {
  withFakeWebSocket(({ lastWs, allWs, flushTimers }) => {
    const client = new WSClient("ws://x");
    lastWs().open();
    client.close();
    lastWs().serverClose(1006);
    flushTimers();
    assert.equal(allWs().length, 1, "no reconnect after close()");
  });
});
