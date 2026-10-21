import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { MsgHeadSchema } from "./gen/aifd/proxy/proxy_pb.js";

export type Packet = {
  topic: string;
  pbType: string;
  inbox: number;
  body: Uint8Array;
};

export function encodePacket(topic: string, pbType: string, inbox: number, body: Uint8Array): Uint8Array<ArrayBuffer> {
  if (!Number.isSafeInteger(inbox) || inbox < 0 || inbox > 0x7fffffff) {
    throw new Error("proxy packet inbox is outside int32 range");
  }
  if (body.byteLength > 0x7fffffff) {
    throw new Error("proxy packet body is outside int32 range");
  }
  const normalizedPBType = pbType || topic;
  if (!normalizedPBType) {
    throw new Error("proxy packet pbType is empty");
  }
  const head = create(MsgHeadSchema, {
    len: body.byteLength,
    inbox,
    topic: topic || normalizedPBType,
    pbType: normalizedPBType
  });
  return concatBytes(toBinary(MsgHeadSchema, head), body);
}

export function decodePacket(input: ArrayBuffer | Uint8Array): Packet {
  const bytes = input instanceof Uint8Array ? input : new Uint8Array(input);
  const bodyLength = readBodyLength(bytes);
  const headLength = bytes.byteLength - bodyLength;
  if (headLength < 0) {
    throw new Error("proxy packet body length exceeds packet length");
  }
  let head;
  try {
    head = fromBinary(MsgHeadSchema, bytes.subarray(0, headLength));
  } catch (error) {
    throw new Error("proxy packet contains an invalid MsgHead", { cause: error });
  }
  if (head.len !== bodyLength) {
    throw new Error("proxy packet body length does not match MsgHead.len");
  }
  if (!head.pbType) {
    throw new Error("proxy packet pbType is empty");
  }
  return {
    topic: head.topic || head.pbType,
    pbType: head.pbType,
    inbox: head.inbox,
    body: bytes.slice(headLength)
  };
}

function readBodyLength(bytes: Uint8Array): number {
  if (bytes.byteLength === 0) {
    throw new Error("proxy packet is empty");
  }
  const key = readVarint(bytes, 0);
  const lenField = MsgHeadSchema.field.len;
  const fieldNumber = Number(key.value >> 3n);
  const wireType = Number(key.value & 7n);
  if (fieldNumber !== lenField.number) {
    return 0;
  }
  if (wireType !== 0) {
    throw new Error("proxy packet MsgHead.len has invalid wire type");
  }
  const length = readVarint(bytes, key.offset).value;
  if (length > BigInt(0x7fffffff)) {
    throw new Error("proxy packet body length is outside int32 range");
  }
  return Number(length);
}

function readVarint(bytes: Uint8Array, start: number): { value: bigint; offset: number } {
  let value = 0n;
  let shift = 0n;
  for (let offset = start; offset < bytes.byteLength && shift <= 63n; offset += 1, shift += 7n) {
    const byte = bytes[offset];
    value |= BigInt(byte & 0x7f) << shift;
    if ((byte & 0x80) === 0) {
      return { value, offset: offset + 1 };
    }
  }
  throw new Error("proxy packet contains an invalid varint");
}

function concatBytes(left: Uint8Array, right: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(left.byteLength + right.byteLength);
  out.set(left, 0);
  out.set(right, left.byteLength);
  return out;
}
