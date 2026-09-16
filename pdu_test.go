package wemodbus

import (
	"bytes"
	"errors"
	"testing"
)

func TestBuildReadRequest(t *testing.T) {
	tests := []struct {
		name     string
		function byte
		address  uint16
		quantity uint16
		want     []byte
	}{
		{"read holding registers", FuncReadHoldingRegisters, 0x006B, 0x0003, []byte{0x03, 0x00, 0x6B, 0x00, 0x03}},
		{"read coils", FuncReadCoils, 0x0013, 0x0025, []byte{0x01, 0x00, 0x13, 0x00, 0x25}},
		{"read discrete inputs", FuncReadDiscreteInputs, 0x00C4, 0x0016, []byte{0x02, 0x00, 0xC4, 0x00, 0x16}},
		{"read input registers", FuncReadInputRegisters, 0x0008, 0x0001, []byte{0x04, 0x00, 0x08, 0x00, 0x01}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildReadRequest(tt.function, tt.address, tt.quantity)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("buildReadRequest = % X, want % X", got, tt.want)
			}
		})
	}
}

func TestBuildWriteSingleRequests(t *testing.T) {
	if got, want := buildWriteSingleCoilRequest(0x00AC, true), []byte{0x05, 0x00, 0xAC, 0xFF, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("coil on = % X, want % X", got, want)
	}
	if got, want := buildWriteSingleCoilRequest(0x00AC, false), []byte{0x05, 0x00, 0xAC, 0x00, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("coil off = % X, want % X", got, want)
	}
	if got, want := buildWriteSingleRegisterRequest(0x0001, 0x0003), []byte{0x06, 0x00, 0x01, 0x00, 0x03}; !bytes.Equal(got, want) {
		t.Fatalf("register = % X, want % X", got, want)
	}
}

func TestBuildWriteMultipleCoilsRequest(t *testing.T) {
	values := make([]bool, 10)
	for i := range values {
		values[i] = true
	}
	got := buildWriteMultipleCoilsRequest(0x0013, values)
	want := []byte{0x0F, 0x00, 0x13, 0x00, 0x0A, 0x02, 0xFF, 0x03}
	if !bytes.Equal(got, want) {
		t.Fatalf("write multiple coils = % X, want % X", got, want)
	}
}

func TestBuildWriteMultipleRegistersRequest(t *testing.T) {
	got := buildWriteMultipleRegistersRequest(0x0001, []uint16{0x000A, 0x0102})
	want := []byte{0x10, 0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0x0A, 0x01, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("write multiple registers = % X, want % X", got, want)
	}
}

func TestBuildMaskWriteRegisterRequest(t *testing.T) {
	got := buildMaskWriteRegisterRequest(0x0004, 0x00F2, 0x0025)
	want := []byte{0x16, 0x00, 0x04, 0x00, 0xF2, 0x00, 0x25}
	if !bytes.Equal(got, want) {
		t.Fatalf("mask write register = % X, want % X", got, want)
	}
}

func TestBuildReadWriteMultipleRegistersRequest(t *testing.T) {
	got := buildReadWriteMultipleRegistersRequest(0x0003, 0x0006, 0x000E, []uint16{0x00FF})
	want := []byte{0x17, 0x00, 0x03, 0x00, 0x06, 0x00, 0x0E, 0x00, 0x01, 0x02, 0x00, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("read write multiple registers = % X, want % X", got, want)
	}
}

func TestPackCoilsIsLSBFirst(t *testing.T) {
	values := []bool{true, false, true, true, false, false, false, true, true}
	got := packCoils(values)
	want := []byte{0x8D, 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("packCoils = % X, want % X", got, want)
	}
	back := unpackCoils(got, uint16(len(values)))
	for i := range values {
		if back[i] != values[i] {
			t.Fatalf("unpackCoils[%d] = %v, want %v", i, back[i], values[i])
		}
	}
}

func TestPackCoilsEmpty(t *testing.T) {
	if got := packCoils(nil); len(got) != 0 {
		t.Fatalf("packCoils(nil) = % X, want empty", got)
	}
}

func TestCheckQuantityLimits(t *testing.T) {
	tests := []struct {
		name     string
		quantity uint16
		min      int
		max      int
		wantErr  bool
	}{
		{"zero is rejected", 0, 1, 125, true},
		{"lower bound", 1, 1, 125, false},
		{"upper bound", 125, 1, 125, false},
		{"above upper bound", 126, 1, 125, true},
		{"max uint16", 0xFFFF, 1, 125, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkQuantity(tt.quantity, tt.min, tt.max)
			if tt.wantErr && !errors.Is(err, ErrQuantity) {
				t.Fatalf("checkQuantity = %v, want ErrQuantity", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("checkQuantity = %v, want nil", err)
			}
		})
	}
}

func TestCheckAddressRange(t *testing.T) {
	if err := checkAddressRange(0xFFFF, 1); err != nil {
		t.Fatalf("checkAddressRange(0xFFFF, 1) = %v, want nil", err)
	}
	if err := checkAddressRange(0xFFFF, 2); !errors.Is(err, ErrQuantity) {
		t.Fatalf("checkAddressRange(0xFFFF, 2) = %v, want ErrQuantity", err)
	}
}

func TestParseBitsResponse(t *testing.T) {
	pdu := []byte{0x01, 0x02, 0xCD, 0x01}
	values, err := parseBitsResponse(pdu, FuncReadCoils, 0x11, 10)
	if err != nil {
		t.Fatalf("parseBitsResponse: %v", err)
	}
	want := []bool{true, false, true, true, false, false, true, true, true, false}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("value[%d] = %v, want %v", i, values[i], want[i])
		}
	}
}

func TestParseBitsResponseByteCountMismatch(t *testing.T) {
	// 10 个线圈需要 2 个字节，但响应只声明了 1 个。
	pdu := []byte{0x01, 0x01, 0xCD, 0x01}
	if _, err := parseBitsResponse(pdu, FuncReadCoils, 0x11, 10); !errors.Is(err, ErrFrame) {
		t.Fatalf("declared byte count mismatch = %v, want ErrFrame", err)
	}
	// 声明了 2 个字节，却只带了 1 个字节的数据。
	pdu = []byte{0x01, 0x02, 0xCD}
	if _, err := parseBitsResponse(pdu, FuncReadCoils, 0x11, 10); !errors.Is(err, ErrFrame) {
		t.Fatalf("truncated data = %v, want ErrFrame", err)
	}
}

func TestParseRegistersResponse(t *testing.T) {
	pdu := []byte{0x03, 0x04, 0x00, 0x0A, 0x01, 0x02}
	registers, err := parseRegistersResponse(pdu, FuncReadHoldingRegisters, 0x11, 2)
	if err != nil {
		t.Fatalf("parseRegistersResponse: %v", err)
	}
	if len(registers) != 2 || registers[0] != 0x000A || registers[1] != 0x0102 {
		t.Fatalf("registers = % X, want [000A 0102]", registers)
	}
}

func TestParseRegistersResponseByteCountMismatch(t *testing.T) {
	pdu := []byte{0x03, 0x02, 0x00, 0x0A}
	if _, err := parseRegistersResponse(pdu, FuncReadHoldingRegisters, 0x11, 2); !errors.Is(err, ErrFrame) {
		t.Fatalf("declared byte count mismatch = %v, want ErrFrame", err)
	}
}

func TestParseEchoResponse(t *testing.T) {
	request := []byte{0x06, 0x00, 0x01, 0x00, 0x03}
	echo := request[1:]
	if err := parseEchoResponse([]byte{0x06, 0x00, 0x01, 0x00, 0x03}, FuncWriteSingleRegister, 0x11, echo); err != nil {
		t.Fatalf("parseEchoResponse on a matching echo: %v", err)
	}
	mismatch := []byte{0x06, 0x00, 0x01, 0x00, 0x04}
	if err := parseEchoResponse(mismatch, FuncWriteSingleRegister, 0x11, echo); !errors.Is(err, ErrFrame) {
		t.Fatalf("parseEchoResponse on a mismatch = %v, want ErrFrame", err)
	}
	short := []byte{0x06, 0x00, 0x01}
	if err := parseEchoResponse(short, FuncWriteSingleRegister, 0x11, echo); !errors.Is(err, ErrFrame) {
		t.Fatalf("parseEchoResponse on a truncated echo = %v, want ErrFrame", err)
	}
}

func TestEchoLength(t *testing.T) {
	for _, function := range []byte{FuncWriteSingleCoil, FuncWriteSingleRegister, FuncWriteMultipleCoils, FuncWriteMultipleRegisters} {
		if got := echoLength(function); got != 4 {
			t.Fatalf("echoLength(0x%02X) = %d, want 4", function, got)
		}
	}
	if got := echoLength(FuncMaskWriteRegister); got != 6 {
		t.Fatalf("echoLength(0x16) = %d, want 6", got)
	}
}

func TestCheckFunctionException(t *testing.T) {
	err := checkFunction([]byte{0x83, byte(ExceptionIllegalDataAddress)}, FuncReadHoldingRegisters, 0x11)
	var ex *ExceptionError
	if !errors.As(err, &ex) {
		t.Fatalf("checkFunction = %v, want *ExceptionError", err)
	}
	if ex.Code != ExceptionIllegalDataAddress || ex.Function != FuncReadHoldingRegisters || ex.UnitID != 0x11 {
		t.Fatalf("ExceptionError = %+v", ex)
	}
	if ex.Error() == "" {
		t.Fatal("ExceptionError.Error() is empty")
	}
	if ExceptionIllegalDataAddress.String() != "illegal data address" {
		t.Fatalf("ExceptionCode.String() = %q", ExceptionIllegalDataAddress.String())
	}
	if ExceptionCode(0x7F).String() == "" {
		t.Fatal("unknown exception code must still have a description")
	}
}

func TestCheckFunctionErrors(t *testing.T) {
	if err := checkFunction(nil, FuncReadHoldingRegisters, 0x11); !errors.Is(err, ErrFrame) {
		t.Fatalf("empty PDU = %v, want ErrFrame", err)
	}
	if err := checkFunction([]byte{0x83}, FuncReadHoldingRegisters, 0x11); !errors.Is(err, ErrFrame) {
		t.Fatalf("truncated exception = %v, want ErrFrame", err)
	}
	if err := checkFunction([]byte{0x04, 0x00}, FuncReadHoldingRegisters, 0x11); !errors.Is(err, ErrFunction) {
		t.Fatalf("wrong function = %v, want ErrFunction", err)
	}
}

func TestRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"timeout", ErrTimeout, true},
		{"crc", ErrCRC, true},
		{"lrc", ErrLRC, true},
		{"frame", ErrFrame, true},
		{"unit id", ErrUnitID, true},
		{"function", ErrFunction, true},
		{"quantity", ErrQuantity, false},
		{"closed", ErrClosed, false},
		{"broadcast", ErrBroadcast, false},
		{"nil", nil, false},
		{"acknowledge", &ExceptionError{Code: ExceptionAcknowledge}, true},
		{"busy", &ExceptionError{Code: ExceptionSlaveDeviceBusy}, true},
		{"illegal address", &ExceptionError{Code: ExceptionIllegalDataAddress}, false},
		{"illegal function", &ExceptionError{Code: ExceptionIllegalFunction}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryable(tt.err); got != tt.want {
				t.Fatalf("retryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
