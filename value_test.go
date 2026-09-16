package wemodbus

import (
	"math"
	"testing"
)

func TestByteOrderString(t *testing.T) {
	want := map[ByteOrder]string{ABCD: "ABCD", CDAB: "CDAB", BADC: "BADC", DCBA: "DCBA"}
	for order, name := range want {
		if got := order.String(); got != name {
			t.Fatalf("ByteOrder(%d).String() = %q, want %q", int(order), got, name)
		}
	}
	if ByteOrder(9).String() == "" {
		t.Fatal("unknown ByteOrder must still have a description")
	}
}

func TestUint32RegisterEncoding(t *testing.T) {
	const value uint32 = 0x12345678
	tests := []struct {
		order ByteOrder
		want  []uint16
	}{
		{ABCD, []uint16{0x1234, 0x5678}},
		{CDAB, []uint16{0x5678, 0x1234}},
		{BADC, []uint16{0x3412, 0x7856}},
		{DCBA, []uint16{0x7856, 0x3412}},
	}
	for _, tt := range tests {
		t.Run(tt.order.String(), func(t *testing.T) {
			got := Uint32ToRegisters(value, tt.order)
			if len(got) != 2 || got[0] != tt.want[0] || got[1] != tt.want[1] {
				t.Fatalf("Uint32ToRegisters = %04X, want %04X", got, tt.want)
			}
			if back := RegistersToUint32(got, tt.order); back != value {
				t.Fatalf("RegistersToUint32 = 0x%08X, want 0x%08X", back, value)
			}
		})
	}
}

func TestInt32RegisterEncoding(t *testing.T) {
	const value int32 = -2
	registers := Int32ToRegisters(value, ABCD)
	if registers[0] != 0xFFFF || registers[1] != 0xFFFE {
		t.Fatalf("Int32ToRegisters(-2) = %04X, want [FFFF FFFE]", registers)
	}
	for _, order := range []ByteOrder{ABCD, CDAB, BADC, DCBA} {
		if back := RegistersToInt32(Int32ToRegisters(value, order), order); back != value {
			t.Fatalf("%s: RegistersToInt32 = %d, want %d", order, back, value)
		}
	}
}

func TestFloat32RegisterEncoding(t *testing.T) {
	const value float32 = 1.5
	if got := Float32ToRegisters(value, ABCD); got[0] != 0x3FC0 || got[1] != 0x0000 {
		t.Fatalf("Float32ToRegisters(1.5) = %04X, want [3FC0 0000]", got)
	}
	for _, order := range []ByteOrder{ABCD, CDAB, BADC, DCBA} {
		if back := RegistersToFloat32(Float32ToRegisters(value, order), order); back != value {
			t.Fatalf("%s: RegistersToFloat32 = %v, want %v", order, back, value)
		}
	}
}

func TestFloat32NegativeRoundTrip(t *testing.T) {
	for _, value := range []float32{0, -0, 1, -1, 12345.678, math.MaxFloat32, math.SmallestNonzeroFloat32} {
		for _, order := range []ByteOrder{ABCD, CDAB, BADC, DCBA} {
			back := RegistersToFloat32(Float32ToRegisters(value, order), order)
			if math.Float32bits(back) != math.Float32bits(value) {
				t.Fatalf("%s: round trip of %v gave %v", order, value, back)
			}
		}
	}
}

func TestUint64RegisterEncoding(t *testing.T) {
	const value uint64 = 0x0123456789ABCDEF
	tests := []struct {
		order ByteOrder
		want  []uint16
	}{
		{ABCD, []uint16{0x0123, 0x4567, 0x89AB, 0xCDEF}},
		{CDAB, []uint16{0x89AB, 0xCDEF, 0x0123, 0x4567}},
		{BADC, []uint16{0x2301, 0x6745, 0xAB89, 0xEFCD}},
		{DCBA, []uint16{0xEFCD, 0xAB89, 0x6745, 0x2301}},
	}
	for _, tt := range tests {
		t.Run(tt.order.String(), func(t *testing.T) {
			got := Uint64ToRegisters(value, tt.order)
			if len(got) != 4 {
				t.Fatalf("Uint64ToRegisters returned %d registers, want 4", len(got))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("Uint64ToRegisters = %04X, want %04X", got, tt.want)
				}
			}
			if back := RegistersToUint64(got, tt.order); back != value {
				t.Fatalf("RegistersToUint64 = 0x%016X, want 0x%016X", back, value)
			}
		})
	}
}

func TestFloat64AndInt64RoundTrip(t *testing.T) {
	const floatValue = -3.141592653589793
	for _, order := range []ByteOrder{ABCD, CDAB, BADC, DCBA} {
		if back := RegistersToFloat64(Float64ToRegisters(floatValue, order), order); back != floatValue {
			t.Fatalf("%s: Float64 round trip = %v, want %v", order, back, floatValue)
		}
	}
	const intValue int64 = -1234567890123
	for _, order := range []ByteOrder{ABCD, CDAB, BADC, DCBA} {
		if back := RegistersToInt64(Int64ToRegisters(intValue, order), order); back != intValue {
			t.Fatalf("%s: Int64 round trip = %d, want %d", order, back, intValue)
		}
	}
}

func TestDecodersPanicOnShortSlice(t *testing.T) {
	assertPanics := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	assertPanics("RegistersToUint32", func() { RegistersToUint32([]uint16{1}, ABCD) })
	assertPanics("RegistersToFloat32", func() { RegistersToFloat32(nil, ABCD) })
	assertPanics("RegistersToUint64", func() { RegistersToUint64([]uint16{1, 2, 3}, ABCD) })
	assertPanics("RegistersToInt64", func() { RegistersToInt64(nil, ABCD) })
}
