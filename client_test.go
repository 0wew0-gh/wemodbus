package wemodbus

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockTransport 模拟一条串口链路：Write 把预置好的响应放进接收缓冲，缓冲为
// 空时 Read 返回 (0, nil)——与 Windows 串口驱动在超时后的行为完全一致，
// 用来确认本包不依赖传输层的超时错误。
type mockTransport struct {
	mu         sync.Mutex
	pending    []byte
	replies    [][]byte
	writes     [][]byte
	writeCalls int
	chunk      int
	resets     int
	writeErr   error
	readErr    error
	closed     bool
}

func (m *mockTransport) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.pending) == 0 {
		return 0, nil
	}
	n := len(m.pending)
	if m.chunk > 0 && n > m.chunk {
		n = m.chunk
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, m.pending[:n])
	m.pending = m.pending[n:]
	return n, nil
}

func (m *mockTransport) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++
	if m.writeErr != nil {
		return 0, m.writeErr
	}
	m.writes = append(m.writes, append([]byte(nil), p...))
	if len(m.replies) > 0 {
		m.pending = append(m.pending, m.replies[0]...)
		m.replies = m.replies[1:]
	}
	return len(p), nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockTransport) SetReadTimeout(time.Duration) error { return nil }

func (m *mockTransport) ResetInputBuffer() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets++
	m.pending = nil
	return nil
}

func (m *mockTransport) writeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeCalls
}

func (m *mockTransport) frame(i int) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i >= len(m.writes) {
		return nil
	}
	return append([]byte(nil), m.writes[i]...)
}

func (m *mockTransport) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// newTestClient 建立一个使用 mockTransport 的客户端，超时默认 50ms。
func newTestClient(t *testing.T, cfg Config, replies ...[]byte) (*Client, *mockTransport) {
	t.Helper()
	if cfg.Timeout == 0 {
		cfg.Timeout = 50 * time.Millisecond
	}
	mt := &mockTransport{replies: replies}
	c := NewClient(mt, cfg)
	t.Cleanup(func() { _ = c.Close() })
	return c, mt
}

// rtuReply 构造一个 RTU 响应帧。
func rtuReply(unitID byte, pdu ...byte) []byte {
	return BuildFrame(ModeRTU, unitID, pdu)
}

// rtuReplyFunc 构造一个功能码与数据分开书写的 RTU 响应帧。
func rtuReplyFunc(unitID, function byte, data ...byte) []byte {
	return BuildFrame(ModeRTU, unitID, append([]byte{function}, data...))
}

func TestNewClientPanicsOnNilTransport(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewClient(nil, ...) did not panic")
		}
	}()
	NewClient(nil, Config{})
}

func TestClientConfigNormalization(t *testing.T) {
	c := NewClient(&mockTransport{}, Config{})
	cfg := c.Config()
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %v, want %v", cfg.Timeout, DefaultTimeout)
	}
	if cfg.Mode != ModeRTU {
		t.Fatalf("Mode = %v, want RTU", cfg.Mode)
	}
	if cfg.Retries != 0 {
		t.Fatalf("Retries = %d, want 0", cfg.Retries)
	}
	if c.UnitID() != 0 || c.Mode() != ModeRTU || c.Retries() != 0 || c.InterFrameDelay() != 0 {
		t.Fatal("unexpected default accessor values")
	}
	if c.Timeout() != DefaultTimeout {
		t.Fatalf("Timeout() = %v, want %v", c.Timeout(), DefaultTimeout)
	}
}

func TestClientSetters(t *testing.T) {
	c := NewClient(&mockTransport{}, Config{})
	c.SetUnitID(7)
	c.SetMode(ModeASCII)
	c.SetTimeout(123 * time.Millisecond)
	c.SetRetries(2)
	c.SetInterFrameDelay(5 * time.Millisecond)
	if c.UnitID() != 7 || c.Mode() != ModeASCII || c.Retries() != 2 {
		t.Fatalf("setters did not stick: %+v", c.Config())
	}
	if c.Timeout() != 123*time.Millisecond || c.InterFrameDelay() != 5*time.Millisecond {
		t.Fatalf("durations did not stick: %+v", c.Config())
	}

	c.SetTimeout(0)
	if c.Timeout() != DefaultTimeout {
		t.Fatalf("SetTimeout(0) = %v, want %v", c.Timeout(), DefaultTimeout)
	}
	c.SetRetries(-3)
	if c.Retries() != 0 {
		t.Fatalf("SetRetries(-3) = %d, want 0", c.Retries())
	}
	c.SetInterFrameDelay(-time.Second)
	if c.InterFrameDelay() != 0 {
		t.Fatalf("SetInterFrameDelay(-1s) = %v, want 0", c.InterFrameDelay())
	}
}

func TestClientCloseIsIdempotent(t *testing.T) {
	mt := &mockTransport{}
	c := NewClient(mt, Config{UnitID: 1})
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !mt.isClosed() {
		t.Fatal("transport was not closed")
	}
	if _, err := c.ReadHoldingRegisters(0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after Close = %v, want ErrClosed", err)
	}
	if mt.writeCount() != 0 {
		t.Fatalf("a closed client performed %d writes", mt.writeCount())
	}
}

func TestReadHoldingRegisters(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x04, 0x00, 0x0A, 0x01, 0x02))
	registers, err := c.ReadHoldingRegisters(0x006B, 2)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if len(registers) != 2 || registers[0] != 0x000A || registers[1] != 0x0102 {
		t.Fatalf("registers = %04X, want [000A 0102]", registers)
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x03, 0x00, 0x6B, 0x00, 0x02}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("request = % X, want % X + CRC", frame, want)
	}
	if err := CheckCRC16(frame); err != nil {
		t.Fatalf("request CRC: %v", err)
	}
}

func TestReadAllFunctionCodes(t *testing.T) {
	t.Run("coils", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadCoils, 0x02, 0xCD, 0x01))
		values, err := c.ReadCoils(0x0013, 10)
		if err != nil {
			t.Fatalf("ReadCoils: %v", err)
		}
		want := []bool{true, false, true, true, false, false, true, true, true, false}
		for i := range want {
			if values[i] != want[i] {
				t.Fatalf("coil[%d] = %v, want %v", i, values[i], want[i])
			}
		}
	})

	t.Run("discrete inputs", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadDiscreteInputs, 0x01, 0x05))
		values, err := c.ReadDiscreteInputs(0x00C4, 3)
		if err != nil {
			t.Fatalf("ReadDiscreteInputs: %v", err)
		}
		if len(values) != 3 || !values[0] || !values[2] || values[1] {
			t.Fatalf("inputs = %v, want [true false true]", values)
		}
	})

	t.Run("input registers", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadInputRegisters, 0x02, 0x00, 0x64))
		registers, err := c.ReadInputRegisters(0x0008, 1)
		if err != nil {
			t.Fatalf("ReadInputRegisters: %v", err)
		}
		if len(registers) != 1 || registers[0] != 100 {
			t.Fatalf("registers = %04X, want [0064]", registers)
		}
	})

	t.Run("read write multiple registers", func(t *testing.T) {
		c, mt := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadWriteMultipleRegisters, 0x04, 0x00, 0xFF, 0x01, 0x02))
		registers, err := c.ReadWriteMultipleRegisters(0x0003, 2, 0x000E, []uint16{0x00FF})
		if err != nil {
			t.Fatalf("ReadWriteMultipleRegisters: %v", err)
		}
		if len(registers) != 2 || registers[0] != 0x00FF || registers[1] != 0x0102 {
			t.Fatalf("registers = %04X, want [00FF 0102]", registers)
		}
		frame := mt.frame(0)
		want := []byte{0x01, 0x17, 0x00, 0x03, 0x00, 0x02, 0x00, 0x0E, 0x00, 0x01, 0x02, 0x00, 0xFF}
		if !bytes.Equal(frame[:len(frame)-2], want) {
			t.Fatalf("request = % X, want % X + CRC", frame, want)
		}
	})
}

func TestWriteFunctionsEcho(t *testing.T) {
	tests := []struct {
		name    string
		request []byte
		reply   []byte
		call    func(c *Client) error
	}{
		{
			name:    "single coil",
			request: []byte{0x01, 0x05, 0x00, 0xAC, 0xFF, 0x00},
			reply:   rtuReplyFunc(0x01, FuncWriteSingleCoil, 0x00, 0xAC, 0xFF, 0x00),
			call:    func(c *Client) error { return c.WriteSingleCoil(0x00AC, true) },
		},
		{
			name:    "single register",
			request: []byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x03},
			reply:   rtuReplyFunc(0x01, FuncWriteSingleRegister, 0x00, 0x01, 0x00, 0x03),
			call:    func(c *Client) error { return c.WriteSingleRegister(0x0001, 0x0003) },
		},
		{
			name:    "multiple coils",
			request: []byte{0x01, 0x0F, 0x00, 0x13, 0x00, 0x02, 0x01, 0x03},
			reply:   rtuReplyFunc(0x01, FuncWriteMultipleCoils, 0x00, 0x13, 0x00, 0x02),
			call:    func(c *Client) error { return c.WriteMultipleCoils(0x0013, []bool{true, true}) },
		},
		{
			name:    "multiple registers",
			request: []byte{0x01, 0x10, 0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0x0A, 0x01, 0x02},
			reply:   rtuReplyFunc(0x01, FuncWriteMultipleRegisters, 0x00, 0x01, 0x00, 0x02),
			call:    func(c *Client) error { return c.WriteMultipleRegisters(0x0001, []uint16{0x000A, 0x0102}) },
		},
		{
			name:    "mask write register",
			request: []byte{0x01, 0x16, 0x00, 0x04, 0x00, 0xF2, 0x00, 0x25},
			reply:   rtuReplyFunc(0x01, FuncMaskWriteRegister, 0x00, 0x04, 0x00, 0xF2, 0x00, 0x25),
			call:    func(c *Client) error { return c.MaskWriteRegister(0x0004, 0x00F2, 0x0025) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mt := newTestClient(t, Config{UnitID: 0x01}, tt.reply)
			if err := tt.call(c); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			frame := mt.frame(0)
			if !bytes.Equal(frame[:len(frame)-2], tt.request) {
				t.Fatalf("request = % X, want % X", frame, tt.request)
			}
		})
	}
}

func TestWriteEchoMismatchIsRejected(t *testing.T) {
	reply := rtuReplyFunc(0x01, FuncWriteSingleRegister, 0x00, 0x01, 0x00, 0x04)
	c, mt := newTestClient(t, Config{UnitID: 0x01}, reply)
	err := c.WriteSingleRegister(0x0001, 0x0003)
	if !errors.Is(err, ErrFrame) {
		t.Fatalf("mismatched echo = %v, want ErrFrame", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 without retries", mt.writeCount())
	}
}

func TestWriteEchoMismatchIsRetried(t *testing.T) {
	bad := rtuReplyFunc(0x01, FuncWriteSingleRegister, 0x00, 0x01, 0x00, 0x04)
	good := rtuReplyFunc(0x01, FuncWriteSingleRegister, 0x00, 0x01, 0x00, 0x03)
	c, mt := newTestClient(t, Config{UnitID: 0x01, Retries: 1}, bad, good)
	if err := c.WriteSingleRegister(0x0001, 0x0003); err != nil {
		t.Fatalf("WriteSingleRegister: %v", err)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("writes = %d, want 2", mt.writeCount())
	}
}

func TestTransportWriteErrorIsNotRetried(t *testing.T) {
	writeErr := errors.New("port is gone")
	mt := &mockTransport{writeErr: writeErr}
	c := NewClient(mt, Config{UnitID: 0x01, Retries: 3, Timeout: 20 * time.Millisecond})
	err := c.WriteSingleRegister(0x0001, 0x0003)
	if !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("write attempts = %d, want 1", mt.writeCount())
	}
}

func TestExceptionResponseIsNotRetried(t *testing.T) {
	reply := rtuReplyFunc(0x01, FuncReadHoldingRegisters|0x80, byte(ExceptionIllegalDataAddress))
	c, mt := newTestClient(t, Config{UnitID: 0x01, Retries: 3}, reply)
	_, err := c.ReadHoldingRegisters(0x006B, 2)
	var ex *ExceptionError
	if !errors.As(err, &ex) {
		t.Fatalf("read = %v, want *ExceptionError", err)
	}
	if ex.Code != ExceptionIllegalDataAddress || ex.UnitID != 0x01 {
		t.Fatalf("ExceptionError = %+v", ex)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 (slave exceptions must not be retried)", mt.writeCount())
	}
}

func TestBusyExceptionIsRetried(t *testing.T) {
	busy := rtuReplyFunc(0x01, FuncReadHoldingRegisters|0x80, byte(ExceptionSlaveDeviceBusy))
	good := rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A)
	c, mt := newTestClient(t, Config{UnitID: 0x01, Retries: 2}, busy, good)
	registers, err := c.ReadHoldingRegisters(0x006B, 1)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if registers[0] != 0x000A {
		t.Fatalf("registers = %04X, want [000A]", registers)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("writes = %d, want 2", mt.writeCount())
	}
}

func TestRetryAfterCRCFailure(t *testing.T) {
	broken := rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A)
	broken[len(broken)-1] ^= 0xFF
	good := rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A)
	c, mt := newTestClient(t, Config{UnitID: 0x01, Retries: 1}, broken, good)
	registers, err := c.ReadHoldingRegisters(0x006B, 1)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if registers[0] != 0x000A {
		t.Fatalf("registers = %04X, want [000A]", registers)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("writes = %d, want 2", mt.writeCount())
	}
}

func TestUnitIDMismatch(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x02, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A))
	_, err := c.ReadHoldingRegisters(0x006B, 1)
	if !errors.Is(err, ErrUnitID) {
		t.Fatalf("read = %v, want ErrUnitID", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", mt.writeCount())
	}
}

func TestFunctionMismatch(t *testing.T) {
	c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadInputRegisters, 0x02, 0x00, 0x0A))
	_, err := c.ReadHoldingRegisters(0x006B, 1)
	if !errors.Is(err, ErrFunction) {
		t.Fatalf("read = %v, want ErrFunction", err)
	}
}

func TestTimeoutWithoutResponse(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01, Timeout: 20 * time.Millisecond})
	_, err := c.ReadHoldingRegisters(0x006B, 1)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("read = %v, want ErrTimeout", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 without retries", mt.writeCount())
	}
}

func TestTimeoutIsRetried(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01, Timeout: 10 * time.Millisecond, Retries: 2})
	_, err := c.ReadHoldingRegisters(0x006B, 1)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("read = %v, want ErrTimeout", err)
	}
	if mt.writeCount() != 3 {
		t.Fatalf("writes = %d, want 3", mt.writeCount())
	}
}

func TestPartialResponseTimesOut(t *testing.T) {
	full := rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A)
	c, _ := newTestClient(t, Config{UnitID: 0x01, Timeout: 20 * time.Millisecond}, full[:3])
	_, err := c.ReadHoldingRegisters(0x006B, 1)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("partial response = %v, want ErrTimeout", err)
	}
}

func TestChunkedReadStillAssemblesFrame(t *testing.T) {
	mt := &mockTransport{
		replies: [][]byte{rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, 0x0A)},
		chunk:   1,
	}
	c := NewClient(mt, Config{UnitID: 0x01, Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = c.Close() })
	registers, err := c.ReadHoldingRegisters(0x006B, 1)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if registers[0] != 0x000A {
		t.Fatalf("registers = %04X, want [000A]", registers)
	}
}

func TestBroadcastWrite(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0})
	if err := c.WriteSingleRegister(0x0001, 0x0003); err != nil {
		t.Fatalf("broadcast write: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1", mt.writeCount())
	}
	frame := mt.frame(0)
	if frame[0] != 0x00 {
		t.Fatalf("broadcast frame unit id = %d, want 0", frame[0])
	}
}

func TestBroadcastReadIsRejected(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0})
	if _, err := c.ReadHoldingRegisters(0, 1); !errors.Is(err, ErrBroadcast) {
		t.Fatalf("broadcast read = %v, want ErrBroadcast", err)
	}
	if _, err := c.ReadCoils(0, 1); !errors.Is(err, ErrBroadcast) {
		t.Fatalf("broadcast coil read = %v, want ErrBroadcast", err)
	}
	if mt.writeCount() != 0 {
		t.Fatalf("broadcast reads performed %d writes", mt.writeCount())
	}
}

func TestASCIIReadAndException(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		c, mt := newTestClient(t, Config{UnitID: 0x01, Mode: ModeASCII}, []byte("noise:010302000AF0\r\n"))
		registers, err := c.ReadHoldingRegisters(0x006B, 1)
		if err != nil {
			t.Fatalf("ReadHoldingRegisters: %v", err)
		}
		if registers[0] != 0x000A {
			t.Fatalf("registers = %04X, want [000A]", registers)
		}
		if got, want := mt.frame(0), []byte(":0103006B000190\r\n"); !bytes.Equal(got, want) {
			t.Fatalf("request = %q, want %q", got, want)
		}
	})

	t.Run("exception", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01, Mode: ModeASCII}, []byte(":0183027A\r\n"))
		_, err := c.ReadHoldingRegisters(0x006B, 1)
		var ex *ExceptionError
		if !errors.As(err, &ex) {
			t.Fatalf("read = %v, want *ExceptionError", err)
		}
		if ex.Code != ExceptionIllegalDataAddress {
			t.Fatalf("exception code = %v", ex.Code)
		}
	})

	t.Run("incomplete frame times out", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01, Mode: ModeASCII, Timeout: 20 * time.Millisecond}, []byte(":01030200"))
		_, err := c.ReadHoldingRegisters(0x006B, 1)
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("incomplete ASCII frame = %v, want ErrTimeout", err)
		}
	})
}

func TestQuantityValidationBlocksTraffic(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01})
	if _, err := c.ReadHoldingRegisters(0, 0); !errors.Is(err, ErrQuantity) {
		t.Fatalf("zero quantity = %v, want ErrQuantity", err)
	}
	if _, err := c.ReadHoldingRegisters(0, MaxReadRegisters+1); !errors.Is(err, ErrQuantity) {
		t.Fatalf("too many registers = %v, want ErrQuantity", err)
	}
	if _, err := c.ReadCoils(0, MaxReadCoils+1); !errors.Is(err, ErrQuantity) {
		t.Fatalf("too many coils = %v, want ErrQuantity", err)
	}
	if err := c.WriteMultipleRegisters(0, nil); !errors.Is(err, ErrQuantity) {
		t.Fatalf("empty register write = %v, want ErrQuantity", err)
	}
	if err := c.WriteMultipleCoils(0, nil); !errors.Is(err, ErrQuantity) {
		t.Fatalf("empty coil write = %v, want ErrQuantity", err)
	}
	if _, err := c.ReadWriteMultipleRegisters(0, 1, 0, make([]uint16, MaxReadWriteWriteRegisters+1)); !errors.Is(err, ErrQuantity) {
		t.Fatalf("too many write registers = %v, want ErrQuantity", err)
	}
	if _, err := c.ReadHoldingRegisters(0xFFFF, 2); !errors.Is(err, ErrQuantity) {
		t.Fatalf("address overflow = %v, want ErrQuantity", err)
	}
	if mt.writeCount() != 0 {
		t.Fatalf("invalid requests performed %d writes", mt.writeCount())
	}
}

func TestConvenienceHelpers(t *testing.T) {
	t.Run("read float32", func(t *testing.T) {
		c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x04, 0x3F, 0xC0, 0x00, 0x00))
		value, err := c.ReadFloat32(0x0010, ABCD)
		if err != nil {
			t.Fatalf("ReadFloat32: %v", err)
		}
		if value != 1.5 {
			t.Fatalf("ReadFloat32 = %v, want 1.5", value)
		}
	})

	t.Run("write uint32", func(t *testing.T) {
		request := buildWriteMultipleRegistersRequest(0x0010, Uint32ToRegisters(0x12345678, CDAB))
		reply := rtuReplyFunc(0x01, FuncWriteMultipleRegisters, 0x00, 0x10, 0x00, 0x02)
		c, mt := newTestClient(t, Config{UnitID: 0x01}, reply)
		if err := c.WriteUint32(0x0010, 0x12345678, CDAB); err != nil {
			t.Fatalf("WriteUint32: %v", err)
		}
		frame := mt.frame(0)
		if want := BuildFrame(ModeRTU, 0x01, request); !bytes.Equal(frame, want) {
			t.Fatalf("request = % X, want % X", frame, want)
		}
	})
}

func TestConcurrentTransactionsAreSerialized(t *testing.T) {
	const goroutines, rounds = 8, 5
	replies := make([][]byte, goroutines*rounds)
	for i := range replies {
		replies[i] = rtuReplyFunc(0x01, FuncReadHoldingRegisters, 0x02, 0x00, byte(i))
	}
	c, mt := newTestClient(t, Config{UnitID: 0x01, Timeout: time.Second}, replies...)

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if _, err := c.ReadHoldingRegisters(0x006B, 1); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read: %v", err)
	}
	if got := mt.writeCount(); got != goroutines*rounds {
		t.Fatalf("writes = %d, want %d", got, goroutines*rounds)
	}
}

func TestReadErrorFromTransportIsReturned(t *testing.T) {
	readErr := errors.New("device disconnected")
	mt := &mockTransport{readErr: readErr}
	c := NewClient(mt, Config{UnitID: 0x01, Timeout: 20 * time.Millisecond})
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.ReadHoldingRegisters(0x006B, 1); !errors.Is(err, readErr) {
		t.Fatalf("read = %v, want %v", err, readErr)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("writes = %d, want 1 (transport errors are not retried)", mt.writeCount())
	}
}
