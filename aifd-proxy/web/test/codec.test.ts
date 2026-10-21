import { describe, expect, it } from "vitest";
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { decodePacket, encodePacket } from "../src/codec.js";
import { RPCErrorSchema } from "../src/gen/aifd/proxy/proxy_pb.js";

// Kept identical to webClientRPCErrorGolden in the Go codec test.
const rpcErrorGolden = "0817107b1a085250434572726f7222085250434572726f720a07494e56414c4944120ce8afb7e6b182e697a0e69588";

describe("proxy packet codec", () => {
  it("matches the Go RPCError packet including Chinese text and inbox", () => {
    const status = create(RPCErrorSchema, { code: "INVALID", message: "请求无效" });
    const body = toBinary(RPCErrorSchema, status);
    const packet = encodePacket("RPCError", "RPCError", 123, body);
    expect(toHex(packet)).toBe(rpcErrorGolden);
    const goldenBytes = new Uint8Array(rpcErrorGolden.match(/../g)!.map((byte) => parseInt(byte, 16)));
    const decoded = decodePacket(goldenBytes);
    expect(decoded).toMatchObject({ topic: "RPCError", pbType: "RPCError", inbox: 123 });
    expect(fromBinary(RPCErrorSchema, decoded.body)).toEqual(status);
  });

  it("matches the Go packet encoding", () => {
    const packet = encodePacket("MseInventoryInfo", "StringValue", 123, new Uint8Array([1, 2, 3]));
    expect(toHex(packet)).toBe(
      "0803107b1a104d7365496e76656e746f7279496e666f220b537472696e6756616c7565010203"
    );
    expect(decodePacket(packet)).toEqual({
      topic: "MseInventoryInfo",
      pbType: "StringValue",
      inbox: 123,
      body: new Uint8Array([1, 2, 3])
    });
  });

  it("normalizes an empty topic to pbType", () => {
    const decoded = decodePacket(encodePacket("", "StringValue", 7, new Uint8Array([9])));
    expect(decoded.topic).toBe("StringValue");
    expect(decoded.pbType).toBe("StringValue");
  });

  it("copies shared-buffer views into an owned WebSocket-compatible buffer", () => {
    const source = new Uint8Array(new SharedArrayBuffer(5));
    source.set([9, 1, 2, 3, 9]);
    const packet = encodePacket("McePing", "McePing", 1, source.subarray(1, 4));
    expect(packet.buffer).toBeInstanceOf(ArrayBuffer);
    source[1] = 8;
    expect(decodePacket(packet).body).toEqual(new Uint8Array([1, 2, 3]));
  });

  it("rejects an inconsistent body length", () => {
    const packet = encodePacket("McePing", "McePing", 1, new Uint8Array([1, 2]));
    const invalid = new Uint8Array(packet.byteLength + 1);
    invalid.set(packet);
    invalid[invalid.length - 1] = 3;
    expect(() => decodePacket(invalid)).toThrow(/body length|invalid/i);
  });
});

function toHex(value: Uint8Array): string {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
