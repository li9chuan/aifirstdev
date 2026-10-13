import { describe, expect, it, vi } from "vitest";
import { create, toBinary } from "@bufbuild/protobuf";
import { ProxyRPCError, ProxyWebSocketClient, RPCErrorSchema } from "../src/index.js";
import { decodePacket, encodePacket } from "../src/codec.js";

describe("ProxyWebSocketClient", () => {
  it.each([
    { code: "INVALID", message: "请求无效", expectedCode: "INVALID" },
    { code: "", message: "服务不可用", expectedCode: "INTERNAL" }
  ])("rejects RPCError with code $expectedCode and preserves its message", async ({ code, message, expectedCode }) => {
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
      webSocketFactory: () => socket as unknown as WebSocket
    });
    socket.open();
    const client = await connected;
    try {
      const success = client.call("McePing", "McePing", new Uint8Array());
      const failure = client.call("MceReject", "MceReject", new Uint8Array());
      const request = decodePacket(socket.sent[1]);
      const body = toBinary(RPCErrorSchema, create(RPCErrorSchema, { code, message }));
      socket.receive(encodePacket("RPCError", "RPCError", request.inbox, body));
      const error = await failure.catch((error: unknown) => error);
      expect(error).toBeInstanceOf(ProxyRPCError);
      expect(error).toMatchObject({ code: expectedCode, message });

      const first = decodePacket(socket.sent[0]);
      socket.receive(encodePacket("MsePong", "MsePong", first.inbox, new Uint8Array()));
      await expect(success).resolves.toMatchObject({ topic: "MsePong", inbox: first.inbox });
    } finally {
      client.close();
    }
  });

  it("rejects a legacy empty RPCError and keeps the connection usable", async () => {
    const socket = new MockWebSocket();
    const stale = vi.fn();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
      webSocketFactory: () => socket as unknown as WebSocket,
      onStaleReply: stale
    });
    socket.open();
    const client = await connected;
    try {
      const response = client.call("McePing", "McePing", new Uint8Array());
      const request = decodePacket(socket.sent[0]);
      const errorPacket = encodePacket("RPCError", "RPCError", request.inbox, new Uint8Array());
      socket.receive(errorPacket);
      await expect(response).rejects.toMatchObject({ code: "INTERNAL", message: "rpc request failed" });

      const success = client.call("McePing", "McePing", new Uint8Array());
      const next = decodePacket(socket.sent[1]);
      expect(next.inbox).not.toBe(request.inbox);
      socket.receive(errorPacket);
      expect(stale).toHaveBeenCalledOnce();
      socket.receive(encodePacket("MsePong", "MsePong", next.inbox, new Uint8Array()));
      await expect(success).resolves.toMatchObject({ topic: "MsePong", inbox: next.inbox, body: new Uint8Array() });
    } finally {
      client.close();
    }
  });

  it("rejects every pending call immediately on a malformed RPCError", async () => {
    vi.useFakeTimers();
    let client: ProxyWebSocketClient | undefined;
    try {
      const socket = new MockWebSocket();
      const connected = ProxyWebSocketClient.connect("ws://example.test/ws", {
        webSocketFactory: () => socket as unknown as WebSocket
      });
      socket.open();
      client = await connected;
      const first = client.call("McePing", "McePing", new Uint8Array());
      const second = client.call("McePing", "McePing", new Uint8Array());
      const results = Promise.allSettled([first, second]);
      const request = decodePacket(socket.sent[0]);
      socket.receive(encodePacket("RPCError", "RPCError", request.inbox, new Uint8Array([0xff])));
      expect(socket.readyState).toBe(3);
      expect(vi.getTimerCount()).toBe(0);
      expect((await results).map((result) => result.status)).toEqual(["rejected", "rejected"]);
    } finally {
      client?.close();
      vi.useRealTimers();
    }
  });

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
