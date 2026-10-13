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

describe("multidevice replies and heartbeat lifecycle", () => {
  it("keeps equal inbox numbers local and dispatches pushes independently", async () => {
    const first = new MockWebSocket();
    const second = new MockWebSocket();
    const pushA = vi.fn();
    const pushB = vi.fn();
    const aPromise = ProxyWebSocketClient.connect("ws://example.test/ws", { webSocketFactory: () => first as unknown as WebSocket, onPush: pushA });
    const bPromise = ProxyWebSocketClient.connect("ws://example.test/ws", { webSocketFactory: () => second as unknown as WebSocket, onPush: pushB });
    first.open(); second.open();
    const [a, b] = await Promise.all([aPromise, bPromise]);
    try {
      let aSettled = false; let bSettled = false;
      const aReply = a.call("MceCommand", "MceCommand", new Uint8Array()).then(packet => { aSettled = true; return packet; });
      const bReply = b.call("MceCommand", "MceCommand", new Uint8Array()).then(packet => { bSettled = true; return packet; });
      const inbox = decodePacket(first.sent[0]).inbox;
      expect(decodePacket(second.sent[0]).inbox).toBe(inbox);
      const changed = encodePacket("MseChanged", "MseChanged", 0, new Uint8Array([1]));
      first.receive(changed); second.receive(changed);
      await Promise.resolve();
      expect(pushA).toHaveBeenCalledOnce(); expect(pushB).toHaveBeenCalledOnce();
      expect(aSettled).toBe(false); expect(bSettled).toBe(false);
      first.receive(encodePacket("MseResult", "MseResult", inbox, new Uint8Array([2])));
      await expect(aReply).resolves.toMatchObject({ body: new Uint8Array([2]) });
      expect(bSettled).toBe(false);
      second.receive(encodePacket("MseResult", "MseResult", inbox, new Uint8Array([3])));
      await expect(bReply).resolves.toMatchObject({ body: new Uint8Array([3]) });
    } finally { a.close(); b.close(); }
  });

  it("times out probes, resets misses on success and reports loss once", async () => {
    vi.useFakeTimers();
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", { webSocketFactory: () => socket as unknown as WebSocket });
    socket.open();
    const client = await connected;
    try {
      const lost = vi.fn(); let count = 0;
      const probe = vi.fn(async () => { if (++count === 2) { return; } throw new Error("business heartbeat rejected"); });
      client.startHeartbeat({ intervalMs: 10, timeoutMs: 5, maxMisses: 2, probe, onLost: lost });
      expect(() => client.startHeartbeat({ probe })).toThrow(/already running/);
      await vi.advanceTimersByTimeAsync(50);
      expect(probe).toHaveBeenCalledTimes(4);
      expect(lost).toHaveBeenCalledOnce();
      expect(vi.getTimerCount()).toBe(0);
    } finally { client.close(); vi.useRealTimers(); }
  });

  it("stopping heartbeat aborts its pending call and suppresses late loss", async () => {
    vi.useFakeTimers();
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", { webSocketFactory: () => socket as unknown as WebSocket });
    socket.open();
    const client = await connected;
    try {
      const lost = vi.fn();
      client.startHeartbeat({ intervalMs: 10, timeoutMs: 100, maxMisses: 1,
        probe: async signal => { await client.call("MceHeartbeat", "MceHeartbeat", new Uint8Array(), 100, signal); }, onLost: lost });
      await vi.advanceTimersByTimeAsync(11);
      expect(socket.sent).toHaveLength(1);
      client.stopHeartbeat();
      await vi.advanceTimersByTimeAsync(200);
      expect(lost).not.toHaveBeenCalled();
      expect(vi.getTimerCount()).toBe(0);
    } finally { client.close(); vi.useRealTimers(); }
  });

  it("bounds a nonresponding probe and reports an unexpected socket close", async () => {
    vi.useFakeTimers();
    const socket = new MockWebSocket();
    const connected = ProxyWebSocketClient.connect("ws://example.test/ws", { webSocketFactory: () => socket as unknown as WebSocket });
    socket.open();
    const client = await connected;
    try {
      const timedOut = vi.fn();
      client.startHeartbeat({ intervalMs: 10, timeoutMs: 5, maxMisses: 1, probe: () => new Promise<void>(() => {}), onLost: timedOut });
      await vi.advanceTimersByTimeAsync(16);
      expect(timedOut).toHaveBeenCalledOnce();
      const disconnected = vi.fn();
      client.startHeartbeat({ probe: async () => {}, onLost: disconnected });
      socket.serverClose();
      expect(disconnected).toHaveBeenCalledOnce();
      expect(vi.getTimerCount()).toBe(0);
    } finally { client.close(); vi.useRealTimers(); }
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
