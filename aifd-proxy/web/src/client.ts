import { fromBinary } from "@bufbuild/protobuf";
import { decodePacket, encodePacket, type Packet } from "./codec.js";
import { RPCErrorSchema } from "./gen/aifd/proxy/proxy_pb.js";

// ProxyRPCError reports a failed RPC reply matched to a request inbox.
export class ProxyRPCError extends Error {
  constructor(readonly code: string, message: string) {
    super(message);
    this.name = "ProxyRPCError";
  }
}

export type ProxyWebSocketClientOptions = {
  openTimeoutMs?: number;
  requestTimeoutMs?: number;
  webSocketFactory?: (url: string) => WebSocket;
  onPush?: (packet: Packet) => void;
  onStaleReply?: (packet: Packet) => void;
};

type PendingRequest = {
  resolve: (packet: Packet) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

const defaultOpenTimeoutMs = 10_000;
const defaultRequestTimeoutMs = 10_000;
const maxInbox = 0x7fffffff;
const websocketOpenState = 1;

// ProxyWebSocketClient owns AIFD packet framing and request/reply inbox lifecycle.
export class ProxyWebSocketClient {
  private readonly pending = new Map<number, PendingRequest>();
  private nextInbox = 1;
  private closed = false;

  private constructor(
    private readonly socket: WebSocket,
    private readonly options: ProxyWebSocketClientOptions
  ) {}

  static async connect(url: string, options: ProxyWebSocketClientOptions = {}): Promise<ProxyWebSocketClient> {
    const socket = (options.webSocketFactory ?? ((target) => new WebSocket(target)))(url);
    socket.binaryType = "arraybuffer";
    const client = new ProxyWebSocketClient(socket, options);
    client.bindSocket();
    await client.waitForOpen(options.openTimeoutMs ?? defaultOpenTimeoutMs);
    return client;
  }

  call(topic: string, pbType: string, body: Uint8Array, timeoutMs = this.options.requestTimeoutMs ?? defaultRequestTimeoutMs) {
    if (this.closed || this.socket.readyState !== websocketOpenState) {
      return Promise.reject(new Error("proxy websocket is not open"));
    }
    const inbox = this.allocateInbox();
    return new Promise<Packet>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(inbox);
        reject(new Error(`proxy request timed out: ${topic}`));
      }, timeoutMs);
      this.pending.set(inbox, { resolve, reject, timer });
      try {
        this.socket.send(encodePacket(topic, pbType, inbox, body));
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(inbox);
        reject(asError(error));
      }
    });
  }

  send(topic: string, pbType: string, body: Uint8Array): void {
    if (this.closed || this.socket.readyState !== websocketOpenState) {
      throw new Error("proxy websocket is not open");
    }
    this.socket.send(encodePacket(topic, pbType, 0, body));
  }

  close(code?: number, reason?: string): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.rejectAll(new Error("proxy websocket closed"));
    this.socket.close(code, reason);
  }

  private bindSocket(): void {
    this.socket.onmessage = (event) => {
      try {
        this.handlePacket(decodePacket(event.data as ArrayBuffer));
      } catch (error) {
        this.rejectAll(asError(error));
        this.close(1002, "invalid proxy packet");
      }
    };
    this.socket.onclose = () => {
      this.closed = true;
      this.rejectAll(new Error("proxy websocket closed"));
    };
    this.socket.onerror = () => {
      if (this.socket.readyState !== websocketOpenState) {
        this.rejectAll(new Error("proxy websocket failed"));
      }
    };
  }

  private waitForOpen(timeoutMs: number): Promise<void> {
    if (this.socket.readyState === websocketOpenState) {
      return Promise.resolve();
    }
    return new Promise((resolve, reject) => {
      const previousOpen = this.socket.onopen;
      const previousError = this.socket.onerror;
      const restoreHandlers = () => {
        this.socket.onopen = previousOpen;
        this.socket.onerror = previousError;
      };
      const timer = setTimeout(() => {
        restoreHandlers();
        this.close(1000, "open timed out");
        reject(new Error("proxy websocket open timed out"));
      }, timeoutMs);
      this.socket.onopen = (event) => {
        clearTimeout(timer);
        restoreHandlers();
        previousOpen?.call(this.socket, event);
        resolve();
      };
      this.socket.onerror = (event) => {
        clearTimeout(timer);
        restoreHandlers();
        previousError?.call(this.socket, event);
        this.close();
        reject(new Error("proxy websocket failed to open"));
      };
    });
  }

  private handlePacket(packet: Packet): void {
    if (packet.inbox === 0) {
      this.options.onPush?.(packet);
      return;
    }
    const pending = this.pending.get(packet.inbox);
    if (!pending) {
      this.options.onStaleReply?.(packet);
      return;
    }
    // Decode before removing the waiter so protocol errors can reject all pending calls.
    let error: ProxyRPCError | undefined;
    if (packet.topic === "RPCError") {
      const status = packet.body.length > 0
        ? fromBinary(RPCErrorSchema, packet.body)
        : { code: "INTERNAL", message: "rpc request failed" };
      error = new ProxyRPCError(status.code || "INTERNAL", status.message);
    }
    this.pending.delete(packet.inbox);
    clearTimeout(pending.timer);
    if (error) {
      pending.reject(error);
    } else {
      pending.resolve(packet);
    }
  }

  private allocateInbox(): number {
    for (let attempts = 0; attempts < maxInbox; attempts += 1) {
      const inbox = this.nextInbox;
      this.nextInbox = inbox === maxInbox ? 1 : inbox + 1;
      if (!this.pending.has(inbox)) {
        return inbox;
      }
    }
    throw new Error("proxy request inbox space exhausted");
  }

  private rejectAll(error: Error): void {
    for (const [inbox, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.reject(error);
      this.pending.delete(inbox);
    }
  }
}

function asError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}
