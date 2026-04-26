package main

import (
	"bytes"
	"testing"
)

func testWriteVarInt(t *testing.T) {
	tests := []struct {
		val      int
		expected []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xFF, 0x01}},
		{2097151, []byte{0xFF, 0xFF, 0x7F}},
	}

	for _, tc := range tests {
		var buf []byte
		buf = writeVarInt(buf, tc.val)
		if !bytes.Equal(buf, tc.expected) {
			t.Errorf("writeVarInt(%d) = %v; want %v", tc.val, buf, tc.expected)
		}
	}
}

func TestCoreLogic(t *testing.T) {
	t.Run("writeVarInt", testWriteVarInt)
}
