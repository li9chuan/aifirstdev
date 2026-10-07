package luban

import (
	"errors"
	"io"
	"math"
	"testing"
)

func TestReadUintLubanCompactEncoding(t *testing.T) {
	tests := []struct {
		data []byte
		want uint32
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x7f}, 0x7f},
		{[]byte{0x80, 0x80}, 0x80},
		{[]byte{0xbf, 0xff}, 0x3fff},
		{[]byte{0xc0, 0x40, 0x00}, 0x4000},
		{[]byte{0xdf, 0xff, 0xff}, 0x1fffff},
		{[]byte{0xe0, 0x20, 0x00, 0x00}, 0x200000},
		{[]byte{0xef, 0xff, 0xff, 0xff}, 0x0fffffff},
		{[]byte{0xf0, 0x10, 0x00, 0x00, 0x00}, 0x10000000},
		{[]byte{0xf0, 0xff, 0xff, 0xff, 0xff}, math.MaxUint32},
	}
	for _, test := range tests {
		buf := WrapByteBuf(test.data)
		got, err := buf.ReadUint()
		if err != nil || got != test.want || buf.Remaining() != 0 {
			t.Fatalf("ReadUint(%x) = %x, %v, remaining %d; want %x", test.data, got, err, buf.Remaining(), test.want)
		}
	}
}

func TestReadUlongLubanCompactEncoding(t *testing.T) {
	tests := []struct {
		data []byte
		want uint64
	}{
		{[]byte{0x7f}, 0x7f},
		{[]byte{0xf1, 0x00, 0x00, 0x00, 0x00}, 0x100000000},
		{[]byte{0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, 0x00ffffffffffffff},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, math.MaxUint64},
	}
	for _, test := range tests {
		buf := WrapByteBuf(test.data)
		got, err := buf.ReadUlong()
		if err != nil || got != test.want || buf.Remaining() != 0 {
			t.Fatalf("ReadUlong(%x) = %x, %v, remaining %d; want %x", test.data, got, err, buf.Remaining(), test.want)
		}
	}
}

func TestReadFixedNumbersAndText(t *testing.T) {
	data := []byte{
		0x01,
		0xfe, 0xff,
		0xfc, 0xff, 0xff, 0xff,
		0xf8, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		0x00, 0x00, 0xc0, 0x3f,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x40,
		0x03, 0xe7, 0x8c, 0xab,
	}
	buf := WrapByteBuf(data)
	if got, err := buf.ReadBool(); err != nil || !got {
		t.Fatalf("ReadBool = %v, %v", got, err)
	}
	if got, err := buf.ReadFshort(); err != nil || got != -2 {
		t.Fatalf("ReadFshort = %v, %v", got, err)
	}
	if got, err := buf.ReadFint(); err != nil || got != -4 {
		t.Fatalf("ReadFint = %v, %v", got, err)
	}
	if got, err := buf.ReadFlong(); err != nil || got != -8 {
		t.Fatalf("ReadFlong = %v, %v", got, err)
	}
	if got, err := buf.ReadFloat(); err != nil || got != 1.5 {
		t.Fatalf("ReadFloat = %v, %v", got, err)
	}
	if got, err := buf.ReadDouble(); err != nil || got != 2.5 {
		t.Fatalf("ReadDouble = %v, %v", got, err)
	}
	if got, err := buf.ReadString(); err != nil || got != "猫" {
		t.Fatalf("ReadString = %q, %v", got, err)
	}
	if buf.Remaining() != 0 {
		t.Fatalf("remaining = %d", buf.Remaining())
	}
}

func TestReadSignedCompactNumbers(t *testing.T) {
	if got, err := WrapByteBuf([]byte{0x01}).ReadSint(); err != nil || got != -1 {
		t.Fatalf("ReadSint = %d, %v", got, err)
	}
	if got, err := WrapByteBuf([]byte{0x03}).ReadSlong(); err != nil || got != -2 {
		t.Fatalf("ReadSlong = %d, %v", got, err)
	}
	if got, err := WrapByteBuf([]byte{0xf0, 0xff, 0xff, 0xff, 0xff}).ReadInt(); err != nil || got != -1 {
		t.Fatalf("ReadInt = %d, %v", got, err)
	}
	if got, err := WrapByteBuf([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}).ReadLong(); err != nil || got != -1 {
		t.Fatalf("ReadLong = %d, %v", got, err)
	}
}

func TestTruncatedAndCorruptInputReturnErrorsWithoutAdvancing(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		read func(*ByteBuf) error
		want error
	}{
		{"empty byte", nil, func(buf *ByteBuf) error { _, err := buf.ReadByte(); return err }, io.ErrUnexpectedEOF},
		{"truncated uint", []byte{0x80}, func(buf *ByteBuf) error { _, err := buf.ReadUint(); return err }, io.ErrUnexpectedEOF},
		{"invalid uint prefix", []byte{0xf1}, func(buf *ByteBuf) error { _, err := buf.ReadUint(); return err }, ErrInvalidData},
		{"oversized length", []byte{0x02, 'a'}, func(buf *ByteBuf) error { _, err := buf.ReadString(); return err }, ErrInvalidData},
		{"invalid utf8", []byte{0x01, 0xff}, func(buf *ByteBuf) error { _, err := buf.ReadString(); return err }, ErrInvalidData},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buf := WrapByteBuf(test.data)
			err := test.read(buf)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, test.want)
			}
			if buf.Offset() != 0 {
				t.Fatalf("offset = %d after error", buf.Offset())
			}
		})
	}
}

func TestReadBytesCopiesPayload(t *testing.T) {
	data := []byte{0x03, 1, 2, 3}
	got, err := WrapByteBuf(data).ReadBytes()
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 9
	if data[1] != 1 {
		t.Fatalf("ReadBytes aliased input: %v", data)
	}
}
