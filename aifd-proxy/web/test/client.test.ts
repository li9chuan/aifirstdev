import { describe, expect, it, vi } from "vitest";
import { ProxyWebSocketClient } from "../src/client.js";
import { decodePacket, encodePacket } from "../src/codec.js";

describe("ProxyWebSocketClient", () => {
  it("matches replies to their request inbox", async () => {
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
      webSocketFactory: () => socket as unknown as WebSocket
    });
    socket.open();
    const client = await connected;

    const response = client.call("McePing", "McePing", new Uint8Array([1]));
    const request = decodePacket(socket.sent[0]);
    socket.receive(encodePacket("MsePong", "MsePong", request.inbox, new Uint8Array([2])));

    await expect(response).resolves.toMatchObject({ topic: "MsePong", inbox: request.inbox });
  });

  it("cleans timed out requests and reports a late reply as stale", async () => {
    vi.useFakeTimers();
    try {
      const socket = new MockWebSocket();
      const stale = vi.fn();
      const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
        webSocketFactory: () => socket as unknown as WebSocket,
        onStaleReply: stale
      });
      socket.open();
      const client = await connected;
      const response = client.call("McePing", "McePing", new Uint8Array(), 10);
      const request = decodePacket(socket.sent[0]);
      const rejection = expect(response).rejects.toThrow(/timed out/);
      await vi.advanceTimersByTimeAsync(11);
      await rejection;

      socket.receive(encodePacket("MsePong", "MsePong", request.inbox, new Uint8Array()));
      expect(stale).toHaveBeenCalledOnce();
    } finally {
      vi.useRealTimers();
    }
  });

  it("rejects all pending calls when the socket closes", async () => {
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
      webSocketFactory: () => socket as unknown as WebSocket
    });
    socket.open();
    const client = await connected;
    const response = client.call("McePing", "McePing", new Uint8Array());
    socket.serverClose();
    await expect(response).rejects.toThrow(/closed/);
  });

  it("closes a socket that does not finish opening", async () => {
    vi.useFakeTimers();
    try {
      const socket = new MockWebSocket();
      const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
        webSocketFactory: () => socket as unknown as WebSocket,
        openTimeoutMs: 10
      });
      const rejection = expect(connected).rejects.toThrow(/open timed out/);
      await vi.advanceTimersByTimeAsync(11);
      await rejection;
      expect(socket.readyState).toBe(3);
    } finally {
      vi.useRealTimers();
    }
  });
});

class MockWebSocket {
  readyState = 0;
  binaryType = "blob";
  onopen: ((event: Event) => unknown) | null = null;
  onmessage: ((event: MessageEvent) => unknown) | null = null;
  onerror: ((event: Event) => unknown) | null = null;
  onclose: ((event: CloseEvent) => unknown) | null = null;
  sent: Uint8Array[] = [];

  send(data: ArrayBufferLike | Blob | ArrayBufferView): void {
    if (!(data instanceof Uint8Array)) {
      throw new Error("expected Uint8Array");
    }
    this.sent.push(data);
  }

  close(): void {
    this.readyState = 3;
  }

  open(): void {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  receive(data: Uint8Array): void {
    this.onmessage?.(new MessageEvent("message", { data: data.buffer }));
  }

  serverClose(): void {
    this.readyState = 3;
    this.onclose?.(new CloseEvent("close"));
  }
}
