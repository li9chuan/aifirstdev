import { describe, expect, it } from "vitest";
import { decodePacket, encodePacket } from "../src/codec.js";

describe("proxy packet codec", () => {
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
