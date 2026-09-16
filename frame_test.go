package wemodbus

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestModeString(t *testing.T) {
	if ModeRTU.String() != "RTU" || ModeASCII.String() != "ASCII" {
		t.Fatalf("unexpected Mode strings: %q %q", ModeRTU.String(), ModeASCII.String())
	}
}

func TestBuildFrameRTU(t *testing.T) {
	frame := BuildFrame(ModeRTU, 0x01, []byte{0x03, 0x00, 0x00, 0x00, 0x0A})
	want := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A, 0xC5, 0xCD}
	if !bytes.Equal(frame, want) {
		t.Fatalf("BuildFrame(RTU) = % X, want % X", frame, want)
	}
}

func TestBuildFrameASCII(t *testing.T) {
	frame := BuildFrame(ModeASCII, 0x01, []byte{0x03, 0x02, 0x00, 0x0A})
	want := []byte(":010302000AF0\r\n")
	if !bytes.Equal(frame, want) {
		t.Fatalf("BuildFrame(ASCII) = %q, want %q", frame, want)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	pdu := []byte{0x03, 0x04, 0x00, 0x0A, 0x01, 0x02}
	for _, mode := range []Mode{ModeRTU, ModeASCII} {
		t.Run(mode.String(), func(t *testing.T) {
			frame := BuildFrame(mode, 0x11, pdu)
			unitID, got, err := ParseFrame(mode, frame)
			if err != nil {
				t.Fatalf("ParseFrame(% X): %v", frame, err)
			}
			if unitID != 0x11 {
				t.Fatalf("ParseFrame unitID = %d, want 17", unitID)
			}
			if !bytes.Equal(got, pdu) {
				t.Fatalf("ParseFrame PDU = % X, want % X", got, pdu)
			}
		})
	}
}

func TestParseFrameRTUErrors(t *testing.T) {
	if _, _, err := ParseFrame(ModeRTU, []byte{0x01, 0x03, 0x00}); !errors.Is(err, ErrFrame) {
		t.Fatalf("short RTU frame = %v, want ErrFrame", err)
	}

	frame := BuildFrame(ModeRTU, 0x01, []byte{0x03, 0x02, 0x00, 0x0A})
	frame[len(frame)-1] ^= 0xFF
	if _, _, err := ParseFrame(ModeRTU, frame); !errors.Is(err, ErrCRC) {
		t.Fatalf("bad CRC frame = %v, want ErrCRC", err)
	}

	oversized := make([]byte, MaxRTUFrameSize+1)
	if _, _, err := ParseFrame(ModeRTU, oversized); !errors.Is(err, ErrFrame) {
		t.Fatalf("oversized RTU frame = %v, want ErrFrame", err)
	}
}

func TestParseFrameASCIIErrors(t *testing.T) {
	valid := BuildFrame(ModeASCII, 0x01, []byte{0x03, 0x02, 0x00, 0x0A})

	t.Run("missing start", func(t *testing.T) {
		if _, _, err := ParseFrame(ModeASCII, valid[1:]); !errors.Is(err, ErrFrame) {
			t.Fatalf("frame without ':' = %v, want ErrFrame", err)
		}
	})

	t.Run("missing crlf", func(t *testing.T) {
		if _, _, err := ParseFrame(ModeASCII, valid[:len(valid)-2]); !errors.Is(err, ErrFrame) {
			t.Fatalf("frame without CRLF = %v, want ErrFrame", err)
		}
	})

	t.Run("odd hex digits", func(t *testing.T) {
		if _, _, err := ParseFrame(ModeASCII, []byte(":010302000AF\r\n")); !errors.Is(err, ErrFrame) {
			t.Fatalf("odd hex frame = %v, want ErrFrame", err)
		}
	})

	t.Run("bad hex digits", func(t *testing.T) {
		if _, _, err := ParseFrame(ModeASCII, []byte(":0103020Z0AF0\r\n")); !errors.Is(err, ErrFrame) {
			t.Fatalf("bad hex frame = %v, want ErrFrame", err)
		}
	})

	t.Run("bad lrc", func(t *testing.T) {
		bad := bytes.Clone(valid)
		bad[len(bad)-4] = '0'
		if _, _, err := ParseFrame(ModeASCII, bad); !errors.Is(err, ErrLRC) {
			t.Fatalf("bad LRC frame = %v, want ErrLRC", err)
		}
	})

	t.Run("lowercase hex is accepted", func(t *testing.T) {
		lower := bytes.ToLower(valid)
		unitID, pdu, err := ParseFrame(ModeASCII, lower)
		if err != nil {
			t.Fatalf("ParseFrame(lowercase) = %v", err)
		}
		if unitID != 0x01 || !bytes.Equal(pdu, []byte{0x03, 0x02, 0x00, 0x0A}) {
			t.Fatalf("ParseFrame(lowercase) = %d, % X", unitID, pdu)
		}
	})
}

func TestRTUResponseLength(t *testing.T) {
	tests := []struct {
		name   string
		header []byte
		want   int
	}{
		{"exception", []byte{0x01, 0x83, 0x02}, 5},
		{"read registers", []byte{0x01, 0x03, 0x04}, 9},
		{"read coils", []byte{0x01, 0x01, 0x01}, 6},
		{"read write multiple", []byte{0x01, 0x17, 0x02}, 7},
		{"write single coil", []byte{0x01, 0x05, 0x00}, 8},
		{"write single register", []byte{0x01, 0x06, 0x00}, 8},
		{"write multiple coils", []byte{0x01, 0x0F, 0x00}, 8},
		{"write multiple registers", []byte{0x01, 0x10, 0x00}, 8},
		{"mask write", []byte{0x01, 0x16, 0x00}, 10},
		{"unsupported function", []byte{0x01, 0x08, 0x00}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rtuResponseLength(tt.header)
			if tt.want == 0 {
				if !errors.Is(err, ErrFrame) {
					t.Fatalf("rtuResponseLength(% X) = %d, %v, want ErrFrame", tt.header, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("rtuResponseLength(% X): %v", tt.header, err)
			}
			if got != tt.want {
				t.Fatalf("rtuResponseLength(% X) = %d, want %d", tt.header, got, tt.want)
			}
		})
	}

	if _, err := rtuResponseLength([]byte{0x01, 0x03}); !errors.Is(err, ErrFrame) {
		t.Fatalf("short header = %v, want ErrFrame", err)
	}
	if _, err := rtuResponseLength([]byte{0x01, 0x03, 0xFF}); !errors.Is(err, ErrFrame) {
		t.Fatalf("oversized byte count = %v, want ErrFrame", err)
	}
}

func TestRTUFrameDelay(t *testing.T) {
	if got := RTUFrameDelay(9600); got < 3*time.Millisecond || got > 5*time.Millisecond {
		t.Fatalf("RTUFrameDelay(9600) = %v, want about 4ms", got)
	}
	if got := RTUFrameDelay(0); got != RTUFrameDelay(DefaultBaudRate) {
		t.Fatalf("RTUFrameDelay(0) = %v, want %v", got, RTUFrameDelay(DefaultBaudRate))
	}
	if got := RTUFrameDelay(115200); got <= 0 || got >= RTUFrameDelay(9600) {
		t.Fatalf("RTUFrameDelay(115200) = %v, want shorter than at 9600", got)
	}
}
