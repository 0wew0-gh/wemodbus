package wemodbus

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// pipeTransport 把 net.Conn 包装成 Transport，并把读超时模拟成串口驱动那样返回
// (0, nil)，用来把主站与从站在内存里对接起来做端到端测试。
type pipeTransport struct {
	conn net.Conn
	mu   sync.Mutex
	d    time.Duration
}

func (p *pipeTransport) Read(b []byte) (int, error) {
	p.mu.Lock()
	d := p.d
	p.mu.Unlock()
	if d > 0 {
		_ = p.conn.SetReadDeadline(time.Now().Add(d))
	}
	n, err := p.conn.Read(b)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return 0, nil // 与 Windows 串口驱动一致：超时返回 0 字节且无错误
		}
		return 0, err
	}
	return n, nil
}

func (p *pipeTransport) Write(b []byte) (int, error) { return p.conn.Write(b) }
func (p *pipeTransport) Close() error                { return p.conn.Close() }

func (p *pipeTransport) SetReadTimeout(d time.Duration) error {
	p.mu.Lock()
	p.d = d
	p.mu.Unlock()
	return nil
}

// startPipe 把 Client 与 Server 用内存管道对接，返回已启动的客户端。
func startPipe(t *testing.T, cfg Config, handler Handler) (*Client, *Server) {
	t.Helper()
	c1, c2 := net.Pipe()
	server := NewServer(&pipeTransport{conn: c2}, ServerConfig{UnitID: cfg.UnitID, Mode: cfg.Mode, Timeout: cfg.Timeout}, handler)
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })

	client := NewClient(&pipeTransport{conn: c1}, cfg)
	t.Cleanup(func() { _ = client.Close() })
	return client, server
}

// slaveMock 是给从站用的内存 Transport：把预置的请求字节喂给 Serve，并记录写出的响应。
type slaveMock struct {
	mu     sync.Mutex
	input  []byte
	output []byte
	closed bool
}

func (m *slaveMock) Read(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("transport closed")
	}
	if len(m.input) == 0 {
		return 0, nil // 总线空闲
	}
	n := copy(p, m.input)
	m.input = m.input[n:]
	return n, nil
}

func (m *slaveMock) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("transport closed")
	}
	m.output = append(m.output, p...)
	return len(p), nil
}

func (m *slaveMock) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *slaveMock) SetReadTimeout(time.Duration) error { return nil }

func (m *slaveMock) waitOutput(t *testing.T, want int) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		out := append([]byte(nil), m.output...)
		m.mu.Unlock()
		if len(out) >= want {
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待从站响应超时")
	return nil
}

// runSlave 用预置的输入字节跑一个从站，返回它与收到的响应。
func runSlave(t *testing.T, cfg ServerConfig, h Handler, input []byte) (*slaveMock, *Server) {
	t.Helper()
	mock := &slaveMock{input: input}
	server := NewServer(mock, cfg, h)
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	return mock, server
}

func newTestModel() *DataModel {
	return NewDataModel(32, 32, 64, 32)
}

func TestSlaveReadHoldingRegistersEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegisters(10, []uint16{100, 200, 300}); err != nil {
		t.Fatalf("SetHoldingRegisters: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	values, err := client.ReadHoldingRegisters(10, 3)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if len(values) != 3 || values[0] != 100 || values[1] != 200 || values[2] != 300 {
		t.Fatalf("values = %v, want [100 200 300]", values)
	}
}

func TestSlaveReadCoilsEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetCoils(5, []bool{true, false, true, true}); err != nil {
		t.Fatalf("SetCoils: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	values, err := client.ReadCoils(5, 4)
	if err != nil {
		t.Fatalf("ReadCoils: %v", err)
	}
	want := []bool{true, false, true, true}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("coil[%d] = %v, want %v", i, values[i], want[i])
		}
	}

	// 离散输入走另一张表。
	if err := model.SetDiscreteInputs(5, []bool{false, true}); err != nil {
		t.Fatalf("SetDiscreteInputs: %v", err)
	}
	inputs, err := client.ReadDiscreteInputs(5, 2)
	if err != nil {
		t.Fatalf("ReadDiscreteInputs: %v", err)
	}
	if inputs[0] != false || inputs[1] != true {
		t.Fatalf("inputs = %v, want [false true]", inputs)
	}
}

func TestSlaveWritesEndToEnd(t *testing.T) {
	model := newTestModel()
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	if err := client.WriteSingleRegister(3, 1234); err != nil {
		t.Fatalf("WriteSingleRegister: %v", err)
	}
	if got, _ := model.HoldingRegister(3); got != 1234 {
		t.Fatalf("寄存器 3 = %d, want 1234", got)
	}

	if err := client.WriteMultipleRegisters(8, []uint16{1, 2, 3}); err != nil {
		t.Fatalf("WriteMultipleRegisters: %v", err)
	}
	values, err := client.ReadHoldingRegisters(8, 3)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if values[0] != 1 || values[1] != 2 || values[2] != 3 {
		t.Fatalf("values = %v, want [1 2 3]", values)
	}

	if err := client.WriteSingleCoil(7, true); err != nil {
		t.Fatalf("WriteSingleCoil: %v", err)
	}
	coils, err := client.ReadCoils(7, 1)
	if err != nil {
		t.Fatalf("ReadCoils: %v", err)
	}
	if !coils[0] {
		t.Fatal("线圈 7 应为 true")
	}

	if err := client.WriteMultipleCoils(20, []bool{true, false, true}); err != nil {
		t.Fatalf("WriteMultipleCoils: %v", err)
	}
	got, err := client.ReadCoils(20, 3)
	if err != nil {
		t.Fatalf("ReadCoils: %v", err)
	}
	if !got[0] || got[1] || !got[2] {
		t.Fatalf("线圈 = %v, want [true false true]", got)
	}
}

func TestSlaveInputRegistersEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetInputRegisters(2, []uint16{0x41D9, 0x5C29}); err != nil {
		t.Fatalf("SetInputRegisters: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	values, err := client.ReadInputRegisters(2, 2)
	if err != nil {
		t.Fatalf("ReadInputRegisters: %v", err)
	}
	if values[0] != 0x41D9 || values[1] != 0x5C29 {
		t.Fatalf("values = % X, want [41D9 5C29]", values)
	}
	if v := RegistersToFloat32(values, ABCD); v != 27.17 {
		t.Fatalf("float32 = %v, want 27.17", v)
	}
}

func TestSlaveIllegalDataAddressException(t *testing.T) {
	model := newTestModel() // 64 个保持寄存器
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	_, err := client.ReadHoldingRegisters(60, 8) // 60+8 > 64
	var ex *ExceptionError
	if !errors.As(err, &ex) {
		t.Fatalf("err = %v, want *ExceptionError", err)
	}
	if ex.Code != ExceptionIllegalDataAddress {
		t.Fatalf("异常码 = %v, want 非法数据地址", ex.Code)
	}

	// 超量请求（126 个寄存器）由从站回非法数据值；客户端的本地校验会先拦住这类
	// 请求，所以这里直接把请求帧喂给从站。
	overRequest := buildRequestFrame(1, []byte{FuncReadHoldingRegisters, 0x00, 0x00, 0x00, 126})
	mock, _ := runSlave(t, ServerConfig{UnitID: 1}, newTestModel(), overRequest)
	out := mock.waitOutput(t, 5)
	if _, pdu, err := ParseFrame(ModeRTU, out); err != nil || pdu[1] != byte(ExceptionIllegalDataValue) {
		t.Fatalf("超量请求应回 0x03，实际 % X", out)
	}
}

func TestSlaveIllegalFunctionException(t *testing.T) {
	model := newTestModel()
	// 0x08（诊断）不在支持列表里，从站应回 0x01。
	request := buildRequestFrame(1, []byte{0x08, 0x00, 0x00, 0x00, 0x00})
	mock, _ := runSlave(t, ServerConfig{UnitID: 1}, model, request)

	out := mock.waitOutput(t, 5)
	unitID, pdu, err := ParseFrame(ModeRTU, out)
	if err != nil {
		t.Fatalf("响应解析失败：%v (% X)", err, out)
	}
	if unitID != 1 || pdu[0] != 0x88 || pdu[1] != byte(ExceptionIllegalFunction) {
		t.Fatalf("响应 = % X, want 01 88 01 + CRC", out)
	}
}

func TestSlaveIgnoresOtherUnitID(t *testing.T) {
	model := newTestModel()
	request := buildRequestFrame(2, []byte{FuncReadHoldingRegisters, 0x00, 0x00, 0x00, 0x01})
	mock, server := runSlave(t, ServerConfig{UnitID: 1}, model, request)

	time.Sleep(50 * time.Millisecond)
	mock.mu.Lock()
	out := len(mock.output)
	mock.mu.Unlock()
	if out != 0 {
		t.Fatal("地址不符的请求不应答")
	}
	if stats := server.Stats(); stats.Requests != 0 || stats.Ignored == 0 {
		t.Fatalf("统计 = %+v, 期望忽略 1 帧", stats)
	}
}

func TestSlaveBroadcastWrite(t *testing.T) {
	model := newTestModel()
	// 广播写单个寄存器：地址 0，执行但不应答。
	request := buildRequestFrame(0, []byte{FuncWriteSingleRegister, 0x00, 0x05, 0x04, 0xD2})
	mock, _ := runSlave(t, ServerConfig{UnitID: 1}, model, request)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v, _ := model.HoldingRegister(5); v == 1234 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if v, _ := model.HoldingRegister(5); v != 1234 {
		t.Fatalf("广播写入未生效，寄存器 5 = %d", v)
	}
	time.Sleep(20 * time.Millisecond)
	mock.mu.Lock()
	out := len(mock.output)
	mock.mu.Unlock()
	if out != 0 {
		t.Fatalf("广播不应答，实际写了 %d 字节", out)
	}
}

func TestSlaveResyncsAfterNoise(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegister(0, 0x1234); err != nil {
		t.Fatalf("SetHoldingRegister: %v", err)
	}
	// 前面塞 3 个噪声字节，从站应丢弃它们并正确回应后面的请求。
	noise := []byte{0xFF, 0x00, 0x7E}
	request := buildRequestFrame(1, []byte{FuncReadHoldingRegisters, 0x00, 0x00, 0x00, 0x01})
	mock, server := runSlave(t, ServerConfig{UnitID: 1}, model, append(noise, request...))

	out := mock.waitOutput(t, 7)
	unitID, pdu, err := ParseFrame(ModeRTU, out)
	if err != nil {
		t.Fatalf("响应解析失败：%v (% X)", err, out)
	}
	if unitID != 1 || len(pdu) != 4 || pdu[0] != FuncReadHoldingRegisters || be16(pdu[2], pdu[3]) != 0x1234 {
		t.Fatalf("响应 = % X, 期望读到 0x1234", out)
	}
	if stats := server.Stats(); stats.Requests != 1 {
		t.Fatalf("统计 = %+v, want 1 个合法请求", stats)
	}
}

func TestSlaveASCIIEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegister(4, 0x00FF); err != nil {
		t.Fatalf("SetHoldingRegister: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Mode: ModeASCII, Timeout: time.Second}, model)

	values, err := client.ReadHoldingRegisters(4, 1)
	if err != nil {
		t.Fatalf("ReadHoldingRegisters(ASCII): %v", err)
	}
	if values[0] != 0x00FF {
		t.Fatalf("values = % X, want [00FF]", values)
	}

	if err := client.WriteSingleRegister(4, 0x1234); err != nil {
		t.Fatalf("WriteSingleRegister(ASCII): %v", err)
	}
	if v, _ := model.HoldingRegister(4); v != 0x1234 {
		t.Fatalf("寄存器 4 = % X, want 1234", v)
	}
}

func TestServerStats(t *testing.T) {
	model := newTestModel()
	client, server := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	if _, err := client.ReadHoldingRegisters(0, 1); err != nil {
		t.Fatalf("ReadHoldingRegisters: %v", err)
	}
	if _, err := client.ReadHoldingRegisters(60, 8); err == nil {
		t.Fatal("越界读取应失败")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if stats := server.Stats(); stats.Responses >= 1 && stats.Exceptions >= 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("统计 = %+v", server.Stats())
}

func TestDataModelBounds(t *testing.T) {
	model := NewDataModel(8, 8, 8, 8)

	if _, err := model.ReadCoils(7, 2); !errors.Is(err, Exception(ExceptionIllegalDataAddress)) {
		t.Fatalf("越界读线圈 = %v, want 非法数据地址", err)
	}
	if err := model.WriteMultipleRegisters(7, []uint16{1, 2}); !errors.Is(err, Exception(ExceptionIllegalDataAddress)) {
		t.Fatalf("越界写寄存器 = %v, want 非法数据地址", err)
	}
	if _, err := model.ReadHoldingRegisters(0, 0); !errors.Is(err, Exception(ExceptionIllegalDataAddress)) {
		t.Fatalf("零长度读 = %v, want 非法数据地址", err)
	}
	if err := model.SetDiscreteInput(8, true); !errors.Is(err, Exception(ExceptionIllegalDataAddress)) {
		t.Fatalf("越界写离散输入 = %v, want 非法数据地址", err)
	}

	coils, discrete, holding, input := model.Sizes()
	if coils != 8 || discrete != 8 || holding != 8 || input != 8 {
		t.Fatalf("Sizes = %d %d %d %d, want 8 8 8 8", coils, discrete, holding, input)
	}
}

func TestHandlerErrorMapping(t *testing.T) {
	// Handler 返回普通错误 -> 0x04；返回 *ExceptionError -> 用它的码。
	request := buildRequestFrame(1, []byte{FuncReadHoldingRegisters, 0x00, 0x00, 0x00, 0x01})

	mock, _ := runSlave(t, ServerConfig{UnitID: 1}, errorHandler{err: errors.New("backend down")}, request)
	out := mock.waitOutput(t, 5)
	if _, pdu, err := ParseFrame(ModeRTU, out); err != nil || pdu[1] != byte(ExceptionSlaveDeviceFailure) {
		t.Fatalf("普通错误应回 0x04，实际 % X", out)
	}

	mock2, _ := runSlave(t, ServerConfig{UnitID: 1}, errorHandler{err: Exception(ExceptionSlaveDeviceBusy)}, request)
	out2 := mock2.waitOutput(t, 5)
	if _, pdu, err := ParseFrame(ModeRTU, out2); err != nil || pdu[1] != byte(ExceptionSlaveDeviceBusy) {
		t.Fatalf("异常应回 0x06，实际 % X", out2)
	}
}

// errorHandler 是所有读取都返回同一个错误的处理器。
type errorHandler struct{ err error }

func (h errorHandler) ReadCoils(uint16, uint16) ([]bool, error)              { return nil, h.err }
func (h errorHandler) ReadDiscreteInputs(uint16, uint16) ([]bool, error)     { return nil, h.err }
func (h errorHandler) ReadHoldingRegisters(uint16, uint16) ([]uint16, error) { return nil, h.err }
func (h errorHandler) ReadInputRegisters(uint16, uint16) ([]uint16, error)   { return nil, h.err }
func (h errorHandler) WriteSingleCoil(uint16, bool) error                    { return h.err }
func (h errorHandler) WriteSingleRegister(uint16, uint16) error              { return h.err }
func (h errorHandler) WriteMultipleCoils(uint16, []bool) error               { return h.err }
func (h errorHandler) WriteMultipleRegisters(uint16, []uint16) error         { return h.err }

func TestSlaveMaskWriteRegisterEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegister(0, 0x00FF); err != nil {
		t.Fatalf("SetHoldingRegister: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	// (0x00FF AND 0x00F2) OR (0x0025 AND NOT 0x00F2) = 0x00F2 OR 0x0005 = 0x00F7
	if err := client.MaskWriteRegister(0, 0x00F2, 0x0025); err != nil {
		t.Fatalf("MaskWriteRegister: %v", err)
	}
	if got, _ := model.HoldingRegister(0); got != 0x00F7 {
		t.Fatalf("寄存器 0 = %X, want 00F7", got)
	}
}

func TestSlaveReadWriteMultipleRegistersEndToEnd(t *testing.T) {
	model := newTestModel()
	if err := model.SetHoldingRegisters(0, []uint16{100, 200}); err != nil {
		t.Fatalf("SetHoldingRegisters: %v", err)
	}
	client, _ := startPipe(t, Config{UnitID: 1, Timeout: time.Second}, model)

	// 先把 10、20 写到地址 8、9，再把地址 0、1 读回来。
	values, err := client.ReadWriteMultipleRegisters(0, 2, 8, []uint16{10, 20})
	if err != nil {
		t.Fatalf("ReadWriteMultipleRegisters: %v", err)
	}
	if len(values) != 2 || values[0] != 100 || values[1] != 200 {
		t.Fatalf("读回 = %v, want [100 200]", values)
	}
	if v, _ := model.HoldingRegister(8); v != 10 {
		t.Fatalf("寄存器 8 = %d, want 10", v)
	}
	if v, _ := model.HoldingRegister(9); v != 20 {
		t.Fatalf("寄存器 9 = %d, want 20", v)
	}
}

func TestSlaveOptionalFunctionsUnsupported(t *testing.T) {
	// errorHandler 只实现了 Handler 的 8 个方法，0x16 / 0x17 应回非法功能码。
	request := buildRequestFrame(1, []byte{FuncMaskWriteRegister, 0x00, 0x00, 0x00, 0xF2, 0x00, 0x25})
	mock, _ := runSlave(t, ServerConfig{UnitID: 1}, errorHandler{}, request)
	out := mock.waitOutput(t, 5)
	if _, pdu, err := ParseFrame(ModeRTU, out); err != nil || pdu[0] != FuncMaskWriteRegister|0x80 || pdu[1] != byte(ExceptionIllegalFunction) {
		t.Fatalf("0x16 未实现时应回 0x01，实际 % X", out)
	}

	request17 := buildRequestFrame(1, []byte{
		FuncReadWriteMultipleRegisters,
		0x00, 0x00, 0x00, 0x01, // 读地址 0、读数量 1
		0x00, 0x08, 0x00, 0x01, // 写地址 8、写数量 1
		0x02, 0x00, 0x0A, // 数据
	})
	mock2, _ := runSlave(t, ServerConfig{UnitID: 1}, errorHandler{}, request17)
	out2 := mock2.waitOutput(t, 5)
	if _, pdu, err := ParseFrame(ModeRTU, out2); err != nil || pdu[1] != byte(ExceptionIllegalFunction) {
		t.Fatalf("0x17 未实现时应回 0x01，实际 % X", out2)
	}
}

// buildRequestFrame 构造一个 RTU 请求帧。
func buildRequestFrame(unitID byte, pdu []byte) []byte {
	return BuildFrame(ModeRTU, unitID, pdu)
}
