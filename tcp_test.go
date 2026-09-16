package wemodbus

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBuildTCPFrame(t *testing.T) {
	pdu := []byte{FuncReadHoldingRegisters, 0x00, 0x6B, 0x00, 0x03}
	frame := BuildTCPFrame(0x1234, 0x11, pdu)
	want := []byte{
		0x12, 0x34, // 事务标识
		0x00, 0x00, // 协议标识
		0x00, 0x06, // 长度 = 单元标识 1 + PDU 5
		0x11,                         // 单元标识
		0x03, 0x00, 0x6B, 0x00, 0x03, // PDU
	}
	if !bytes.Equal(frame, want) {
		t.Fatalf("BuildTCPFrame = % X, want % X", frame, want)
	}
}

func TestParseTCPFrame(t *testing.T) {
	pdu := []byte{FuncWriteSingleRegister, 0x00, 0x01, 0x00, 0x03}
	frame := BuildTCPFrame(0xBEEF, 0x02, pdu)

	tid, unitID, got, err := ParseTCPFrame(frame)
	if err != nil {
		t.Fatalf("ParseTCPFrame: %v", err)
	}
	if tid != 0xBEEF || unitID != 0x02 || !bytes.Equal(got, pdu) {
		t.Fatalf("ParseTCPFrame = %04X %d % X", tid, unitID, got)
	}
}

func TestParseTCPFrameErrors(t *testing.T) {
	valid := BuildTCPFrame(1, 1, []byte{FuncReadHoldingRegisters, 0x00, 0x00, 0x00, 0x01})

	t.Run("too short", func(t *testing.T) {
		if _, _, _, err := ParseTCPFrame(valid[:5]); !errors.Is(err, ErrFrame) {
			t.Fatalf("err = %v, want ErrFrame", err)
		}
	})

	t.Run("bad protocol id", func(t *testing.T) {
		bad := bytes.Clone(valid)
		bad[3] = 1
		if _, _, _, err := ParseTCPFrame(bad); !errors.Is(err, ErrFrame) {
			t.Fatalf("err = %v, want ErrFrame", err)
		}
	})

	t.Run("length mismatch", func(t *testing.T) {
		bad := bytes.Clone(valid)
		bad[5] = 0x09 // 声明 9 字节，实际 6
		if _, _, _, err := ParseTCPFrame(bad); !errors.Is(err, ErrFrame) {
			t.Fatalf("err = %v, want ErrFrame", err)
		}
	})

	t.Run("zero length", func(t *testing.T) {
		bad := bytes.Clone(valid)
		bad[4], bad[5] = 0, 1
		if _, _, _, err := ParseTCPFrame(bad); !errors.Is(err, ErrFrame) {
			t.Fatalf("err = %v, want ErrFrame", err)
		}
	})
}

// startTCPPipe 用内存管道把 ModeTCP 的 Client 与 Server 对接起来。
func startTCPPipe(t *testing.T, unitID byte, handler Handler) (*Client, *Server) {
	t.Helper()
	c1, c2 := net.Pipe()
	server := NewServer(NewTCPTransport(c2), ServerConfig{UnitID: unitID, Mode: ModeTCP, Timeout: time.Second}, handler)
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })

	client := NewClient(NewTCPTransport(c1), Config{UnitID: unitID, Mode: ModeTCP, Timeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	return client, server
}

func TestTCPEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegisters(0, []uint16{11, 22, 33}); err != nil {
		t.Fatalf("SetHoldingRegisters: %v", err)
	}
	client, _ := startTCPPipe(t, 1, model)

	values, err := client.ReadHoldingRegisters(0, 3)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if values[0] != 11 || values[1] != 22 || values[2] != 33 {
		t.Fatalf("values = %v, want [11 22 33]", values)
	}

	// 写入走 0x06，回显校验同样生效。
	if err := client.WriteSingleRegister(5, 4321); err != nil {
		t.Fatalf("WriteSingleRegister: %v", err)
	}
	if v, _ := model.HoldingRegister(5); v != 4321 {
		t.Fatalf("寄存器 5 = %d, want 4321", v)
	}

	// 异常响应也走 TCP 通道。
	_, err = client.ReadHoldingRegisters(60, 8)
	var ex *ExceptionError
	if !errors.As(err, &ex) || ex.Code != ExceptionIllegalDataAddress {
		t.Fatalf("err = %v, want 非法数据地址异常", err)
	}
}

func TestTCPTransactionIDIncrements(t *testing.T) {
	model := newTestModel()
	client, _ := startTCPPipe(t, 1, model)

	for i := 0; i < 3; i++ {
		if _, err := client.ReadHoldingRegisters(0, 1); err != nil {
			t.Fatalf("第 %d 次读取失败：%v", i+1, err)
		}
	}
	if client.tid != 3 {
		t.Fatalf("事务标识 = %d, want 3", client.tid)
	}
}

func TestTCPRejectsMismatchedTransactionID(t *testing.T) {
	// 直接回一个事务标识不匹配的响应，客户端应报 ErrFrame。
	mock := &mockTransport{replies: [][]byte{BuildTCPFrame(99, 1, []byte{FuncReadHoldingRegisters, 0x02, 0x00, 0x0A})}}
	client := NewClient(mock, Config{UnitID: 1, Mode: ModeTCP, Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.ReadHoldingRegisters(0, 1); !errors.Is(err, ErrFrame) {
		t.Fatalf("err = %v, want ErrFrame", err)
	}
}

func TestTCPSlaveBroadcast(t *testing.T) {
	// TCP 下单元标识 0 同样是广播：执行但不应答。
	model := newTestModel()
	request := BuildTCPFrame(7, 0, []byte{FuncWriteSingleRegister, 0x00, 0x02, 0x00, 0x63})
	mock, _ := runSlave(t, ServerConfig{UnitID: 1, Mode: ModeTCP}, model, request)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, _ := model.HoldingRegister(2); v == 99 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if v, _ := model.HoldingRegister(2); v != 99 {
		t.Fatalf("广播写入未生效，寄存器 2 = %d", v)
	}
	time.Sleep(20 * time.Millisecond)
	mock.mu.Lock()
	out := len(mock.output)
	mock.mu.Unlock()
	if out != 0 {
		t.Fatalf("广播不应答，实际写了 %d 字节", out)
	}
}

func TestTCPModeString(t *testing.T) {
	if got := ModeTCP.String(); got != "TCP" {
		t.Fatalf("ModeTCP.String() = %q", got)
	}
}
