package wemodbus

import (
	"errors"
	"testing"
)

func TestCRC16KnownVectors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint16
	}{
		{"check vector", []byte("123456789"), 0x4B37},
		{"read holding registers request", []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A}, 0xCDC5},
		{"empty", nil, 0xFFFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CRC16(tt.data); got != tt.want {
				t.Fatalf("CRC16(% X) = 0x%04X, want 0x%04X", tt.data, got, tt.want)
			}
		})
	}
}

func TestAppendCRC16WireOrder(t *testing.T) {
	frame := AppendCRC16([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A})
	want := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A, 0xC5, 0xCD}
	if len(frame) != len(want) {
		t.Fatalf("AppendCRC16 returned %d bytes, want %d", len(frame), len(want))
	}
	for i := range want {
		if frame[i] != want[i] {
			t.Fatalf("AppendCRC16 = % X, want % X", frame, want)
		}
	}
}

func TestAppendCRC16DoesNotModifyInput(t *testing.T) {
	data := []byte{0x01, 0x03}
	_ = AppendCRC16(data)
	if len(data) != 2 || data[0] != 0x01 || data[1] != 0x03 {
		t.Fatalf("AppendCRC16 modified its input: % X", data)
	}
}

func TestCheckCRC16RoundTrip(t *testing.T) {
	frame := AppendCRC16([]byte{0x11, 0x03, 0x02, 0x00, 0x0A})
	if err := CheckCRC16(frame); err != nil {
		t.Fatalf("CheckCRC16 on a valid frame: %v", err)
	}
}

func TestCheckCRC16RejectsTamperedFrame(t *testing.T) {
	frame := AppendCRC16([]byte{0x11, 0x03, 0x02, 0x00, 0x0A})
	frame[4] ^= 0x01
	if err := CheckCRC16(frame); !errors.Is(err, ErrCRC) {
		t.Fatalf("CheckCRC16 on a tampered frame = %v, want ErrCRC", err)
	}
}

func TestCheckCRC16ShortFrame(t *testing.T) {
	if err := CheckCRC16([]byte{0x01, 0x02}); !errors.Is(err, ErrFrame) {
		t.Fatalf("CheckCRC16 on a 2-byte frame = %v, want ErrFrame", err)
	}
}

func TestLRCVectors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want byte
	}{
		{"read request", []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A}, 0xF2},
		{"zero sum", []byte{0x00}, 0x00},
		{"high bit does not overflow", []byte{0x80}, 0x80},
		{"carry", []byte{0xFF}, 0x01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LRC(tt.data); got != tt.want {
				t.Fatalf("LRC(% X) = 0x%02X, want 0x%02X", tt.data, got, tt.want)
			}
		})
	}
}

func TestCheckLRCRoundTrip(t *testing.T) {
	body := []byte{0x01, 0x03, 0x02, 0x00, 0x0A}
	frame := append(append([]byte{}, body...), LRC(body))
	if err := CheckLRC(frame); err != nil {
		t.Fatalf("CheckLRC on a valid frame: %v", err)
	}
	frame[2]++
	if err := CheckLRC(frame); !errors.Is(err, ErrLRC) {
		t.Fatalf("CheckLRC on a tampered frame = %v, want ErrLRC", err)
	}
}

func TestCheckLRCShortFrame(t *testing.T) {
	if err := CheckLRC([]byte{0x01}); !errors.Is(err, ErrFrame) {
		t.Fatalf("CheckLRC on a 1-byte frame = %v, want ErrFrame", err)
	}
}
