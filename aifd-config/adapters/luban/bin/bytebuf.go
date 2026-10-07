package luban

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

var ErrInvalidData = errors.New("invalid Luban binary data")

type DecodeError struct {
	Operation string
	Offset    int
	Need      int
	Remaining int
	Cause     error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("luban %s at offset %d: need %d bytes, remaining %d: %v", e.Operation, e.Offset, e.Need, e.Remaining, e.Cause)
}

func (e *DecodeError) Unwrap() error { return e.Cause }

type ByteBuf struct {
	data   []byte
	offset int
}

func WrapByteBuf(data []byte) *ByteBuf {
	return &ByteBuf{data: data}
}

func (b *ByteBuf) Offset() int {
	if b == nil {
		return 0
	}
	return b.offset
}

func (b *ByteBuf) Remaining() int {
	if b == nil || b.offset >= len(b.data) {
		return 0
	}
	return len(b.data) - b.offset
}

func (b *ByteBuf) require(operation string, size int) error {
	remaining := b.Remaining()
	if b == nil || size < 0 || remaining < size {
		return &DecodeError{Operation: operation, Offset: b.Offset(), Need: size, Remaining: remaining, Cause: io.ErrUnexpectedEOF}
	}
	return nil
}

func (b *ByteBuf) invalid(operation string, start int, need int) error {
	return &DecodeError{Operation: operation, Offset: start, Need: need, Remaining: b.Remaining(), Cause: ErrInvalidData}
}

func (b *ByteBuf) ReadBool() (bool, error) {
	value, err := b.ReadByte()
	return value != 0, err
}

func (b *ByteBuf) ReadByte() (byte, error) {
	if err := b.require("byte", 1); err != nil {
		return 0, err
	}
	value := b.data[b.offset]
	b.offset++
	return value, nil
}

func (b *ByteBuf) ReadShort() (int16, error) {
	start := b.Offset()
	if err := b.require("short", 1); err != nil {
		return 0, err
	}
	head := b.data[start]
	switch {
	case head < 0x80:
		b.offset++
		return int16(head), nil
	case head < 0xc0:
		if err := b.require("short", 2); err != nil {
			return 0, err
		}
		b.offset += 2
		return int16(uint16(head&0x3f)<<8 | uint16(b.data[start+1])), nil
	case head == 0xff:
		if err := b.require("short", 3); err != nil {
			return 0, err
		}
		b.offset += 3
		return int16(binary.BigEndian.Uint16(b.data[start+1 : start+3])), nil
	default:
		return 0, b.invalid("short", start, 1)
	}
}

func (b *ByteBuf) ReadFshort() (int16, error) {
	if err := b.require("fixed short", 2); err != nil {
		return 0, err
	}
	value := int16(binary.LittleEndian.Uint16(b.data[b.offset : b.offset+2]))
	b.offset += 2
	return value, nil
}

func (b *ByteBuf) ReadInt() (int32, error) {
	value, err := b.ReadUint()
	return int32(value), err
}

func (b *ByteBuf) ReadUint() (uint32, error) {
	start := b.Offset()
	if err := b.require("uint", 1); err != nil {
		return 0, err
	}
	head := b.data[start]
	var size int
	var value uint32
	switch {
	case head < 0x80:
		size, value = 1, uint32(head)
	case head < 0xc0:
		size, value = 2, uint32(head&0x3f)
	case head < 0xe0:
		size, value = 3, uint32(head&0x1f)
	case head < 0xf0:
		size, value = 4, uint32(head&0x0f)
	case head == 0xf0:
		size, value = 5, 0
	default:
		return 0, b.invalid("uint", start, 1)
	}
	if err := b.require("uint", size); err != nil {
		return 0, err
	}
	for i := 1; i < size; i++ {
		value = value<<8 | uint32(b.data[start+i])
	}
	b.offset += size
	return value, nil
}

func (b *ByteBuf) ReadFint() (int32, error) {
	if err := b.require("fixed int", 4); err != nil {
		return 0, err
	}
	value := int32(binary.LittleEndian.Uint32(b.data[b.offset : b.offset+4]))
	b.offset += 4
	return value, nil
}

func (b *ByteBuf) ReadLong() (int64, error) {
	value, err := b.ReadUlong()
	return int64(value), err
}

func (b *ByteBuf) ReadUlong() (uint64, error) {
	start := b.Offset()
	if err := b.require("ulong", 1); err != nil {
		return 0, err
	}
	head := b.data[start]
	var size int
	var value uint64
	switch {
	case head < 0x80:
		size, value = 1, uint64(head)
	case head < 0xc0:
		size, value = 2, uint64(head&0x3f)
	case head < 0xe0:
		size, value = 3, uint64(head&0x1f)
	case head < 0xf0:
		size, value = 4, uint64(head&0x0f)
	case head < 0xf8:
		size, value = 5, uint64(head&0x07)
	case head < 0xfc:
		size, value = 6, uint64(head&0x03)
	case head < 0xfe:
		size, value = 7, uint64(head&0x01)
	case head == 0xfe:
		size, value = 8, 0
	default:
		size, value = 9, 0
	}
	if err := b.require("ulong", size); err != nil {
		return 0, err
	}
	for i := 1; i < size; i++ {
		value = value<<8 | uint64(b.data[start+i])
	}
	b.offset += size
	return value, nil
}

func (b *ByteBuf) ReadFlong() (int64, error) {
	if err := b.require("fixed long", 8); err != nil {
		return 0, err
	}
	value := int64(binary.LittleEndian.Uint64(b.data[b.offset : b.offset+8]))
	b.offset += 8
	return value, nil
}

func (b *ByteBuf) ReadFloat() (float32, error) {
	value, err := b.ReadFint()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(uint32(value)), nil
}

func (b *ByteBuf) ReadDouble() (float64, error) {
	value, err := b.ReadFlong()
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(uint64(value)), nil
}

func (b *ByteBuf) ReadSize() (int, error) {
	start := b.Offset()
	value, err := b.ReadUint()
	if err != nil {
		return 0, err
	}
	if uint64(value) > uint64(b.Remaining()) {
		b.offset = start
		return 0, b.invalid("size", start, int(value))
	}
	return int(value), nil
}

func (b *ByteBuf) ReadSint() (int32, error) {
	value, err := b.ReadUint()
	if err != nil {
		return 0, err
	}
	return int32(value>>1) ^ -int32(value&1), nil
}

func (b *ByteBuf) ReadSlong() (int64, error) {
	value, err := b.ReadUlong()
	if err != nil {
		return 0, err
	}
	return int64(value>>1) ^ -int64(value&1), nil
}

func (b *ByteBuf) ReadString() (string, error) {
	start := b.Offset()
	size, err := b.ReadSize()
	if err != nil {
		return "", err
	}
	data := b.data[b.offset : b.offset+size]
	if !utf8.Valid(data) {
		b.offset = start
		return "", b.invalid("string", start, size)
	}
	b.offset += size
	return string(data), nil
}

func (b *ByteBuf) ReadBytes() ([]byte, error) {
	size, err := b.ReadSize()
	if err != nil {
		return nil, err
	}
	value := make([]byte, size)
	copy(value, b.data[b.offset:b.offset+size])
	b.offset += size
	return value, nil
}
