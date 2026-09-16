package wemodbus

import (
	"errors"
	"sync"
	"time"
)

const (
	// DefaultTimeout 是 Config.Timeout 为零值时使用的响应超时。
	DefaultTimeout = 500 * time.Millisecond
	// DefaultBaudRate 是 SerialConfig.BaudRate 为零值时使用的波特率。
	DefaultBaudRate = 9600
	// DefaultDataBits 是 SerialConfig.DataBits 为零值时使用的数据位。
	DefaultDataBits = 8

	// rtuCharBits 是 RTU 模式下单个字符的位数：1 起始位 + 8 数据位 + 1 校验位 + 1 停止位。
	rtuCharBits = 11
	// rtuFrameGapChars 是 RTU 规定的帧间静默长度，即 3.5 个字符时间。
	rtuFrameGapChars = 3.5

	// zeroReadPause 是传输层返回 (0, nil) 时的让步时间，避免忙等。
	zeroReadPause = time.Millisecond
)

// RTUFrameDelay 返回给定波特率下的 3.5 个字符时间。波特率不为正时按
// DefaultBaudRate 计算（9600 时约 4.01ms）。
func RTUFrameDelay(baudRate int) time.Duration {
	if baudRate <= 0 {
		baudRate = DefaultBaudRate
	}
	return time.Duration(rtuFrameGapChars * rtuCharBits / float64(baudRate) * float64(time.Second))
}

// Config 是一次事务的参数。零值即 RTU 模式、500ms 超时、不重试。
type Config struct {
	// UnitID 是从站地址。为 0 表示广播：只发送不接收，读操作会返回 ErrBroadcast。
	UnitID byte
	// Mode 是传输模式，零值为 ModeRTU。
	Mode Mode
	// Timeout 是等待完整响应的上限，零值取 DefaultTimeout。
	Timeout time.Duration
	// Retries 是失败后的重试次数，总共尝试 Retries+1 次，零值不重试。
	Retries int
	// InterFrameDelay 是两帧之间的静默时间。Open 在 RTU 模式下若该项为 0，
	// 会按波特率自动取 3.5 个字符时间；ASCII 模式有明确的帧界，保持 0。
	// NewClient 无法得知波特率，因此需要时请显式设置。
	InterFrameDelay time.Duration
}

// normalize 补齐零值并夹取非法值。
func (cfg Config) normalize() Config {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	return cfg
}

// Client 是 Modbus 主站。同一 Client 可被多个 goroutine 并发使用，
// 事务在内部串行化，因为串行链路是半双工总线。
type Client struct {
	mu     sync.Mutex
	t      Transport
	cfg    Config
	closed bool
}

// Open 按 sc 打开 portName 串口并建立客户端，portName 会覆盖 sc.PortName。
func Open(portName string, sc SerialConfig, cfg Config) (*Client, error) {
	sc.PortName = portName
	t, err := OpenSerial(sc)
	if err != nil {
		return nil, err
	}
	cfg = cfg.normalize()
	if cfg.InterFrameDelay == 0 && cfg.Mode == ModeRTU {
		cfg.InterFrameDelay = RTUFrameDelay(sc.BaudRate)
	}
	return &Client{t: t, cfg: cfg}, nil
}

// NewClient 用已有的 Transport 建立客户端，便于测试或非串口链路。
func NewClient(t Transport, cfg Config) *Client {
	if t == nil {
		panic("wemodbus: nil transport")
	}
	return &Client{t: t, cfg: cfg.normalize()}
}

// Close 关闭底层 Transport。重复调用返回 nil。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.t.Close()
}

// Config 返回当前配置的副本。
func (c *Client) Config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// UnitID 返回从站地址。
func (c *Client) UnitID() byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.UnitID
}

// SetUnitID 设置从站地址，0 表示广播。
func (c *Client) SetUnitID(unitID byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.UnitID = unitID
}

// Mode 返回传输模式。
func (c *Client) Mode() Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Mode
}

// SetMode 设置传输模式。
func (c *Client) SetMode(mode Mode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Mode = mode
}

// Timeout 返回响应超时。
func (c *Client) Timeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Timeout
}

// SetTimeout 设置响应超时，非正值取 DefaultTimeout。
func (c *Client) SetTimeout(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		d = DefaultTimeout
	}
	c.cfg.Timeout = d
}

// Retries 返回重试次数。
func (c *Client) Retries() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Retries
}

// SetRetries 设置重试次数，负值按 0 处理。
func (c *Client) SetRetries(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 0 {
		n = 0
	}
	c.cfg.Retries = n
}

// InterFrameDelay 返回帧间静默时间。
func (c *Client) InterFrameDelay() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.InterFrameDelay
}

// SetInterFrameDelay 设置帧间静默时间。取值大意味着更保守的总线静默。
func (c *Client) SetInterFrameDelay(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < 0 {
		d = 0
	}
	c.cfg.InterFrameDelay = d
}

// transact 执行一次带重试的事务。wantResponse 为 false 时允许广播写。
//
// 广播（UnitID 为 0）只发送不接收，成功时返回 (nil, nil)：调用方以 nil 响应
// 表示「本次没有响应可校验」。
func (c *Client) transact(pdu []byte, wantResponse bool) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, ErrClosed
	}
	if c.cfg.UnitID == 0 {
		if wantResponse {
			return nil, ErrBroadcast
		}
		return nil, c.broadcast(pdu)
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		resp, err := c.roundTrip(pdu)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// roundTrip 发送一帧并读取、校验响应，返回响应 PDU。
func (c *Client) roundTrip(pdu []byte) ([]byte, error) {
	frame := BuildFrame(c.cfg.Mode, c.cfg.UnitID, pdu)
	if c.cfg.InterFrameDelay > 0 {
		time.Sleep(c.cfg.InterFrameDelay)
	}
	c.resetInputBuffer()
	if _, err := c.t.Write(frame); err != nil {
		return nil, errorWrap(err, "write failed")
	}
	adu, err := c.readFrame()
	if err != nil {
		return nil, err
	}
	unitID, respPDU, err := ParseFrame(c.cfg.Mode, adu)
	if err != nil {
		return nil, err
	}
	if unitID != c.cfg.UnitID {
		return nil, fail(ErrUnitID, "got %d, want %d", unitID, c.cfg.UnitID)
	}
	// 功能码、从站异常与写类回显都在这里检查，重试策略因此能覆盖它们。
	if err := checkFunction(respPDU, pdu[0], c.cfg.UnitID); err != nil {
		return nil, err
	}
	if isWriteFunction(pdu[0]) {
		n := echoLength(pdu[0])
		if len(pdu) < 1+n {
			return nil, fail(ErrFrame, "request PDU of %d bytes cannot be echoed", len(pdu))
		}
		if err := parseEchoResponse(respPDU, pdu[0], c.cfg.UnitID, pdu[1:1+n]); err != nil {
			return nil, err
		}
	}
	return respPDU, nil
}

// broadcast 发送广播请求，并按帧间静默等待从站处理，不读取响应。
func (c *Client) broadcast(pdu []byte) error {
	frame := BuildFrame(c.cfg.Mode, 0, pdu)
	c.resetInputBuffer()
	if _, err := c.t.Write(frame); err != nil {
		return errorWrap(err, "write failed")
	}
	if c.cfg.InterFrameDelay > 0 {
		time.Sleep(c.cfg.InterFrameDelay)
	}
	return nil
}

// resetInputBuffer 在发送前尽力清空接收缓冲。
func (c *Client) resetInputBuffer() {
	if r, ok := c.t.(InputBufferResetter); ok {
		_ = r.ResetInputBuffer()
	}
}

// readFrame 按当前模式读取一个完整 ADU，超时由本包自行维护。
func (c *Client) readFrame() ([]byte, error) {
	deadline := time.Now().Add(c.cfg.Timeout)
	if c.cfg.Mode == ModeASCII {
		return c.readASCIIFrame(deadline)
	}
	return c.readRTUFrame(deadline)
}

// readChunk 从 Transport 读取一次数据。
//
// 返回 (nil, nil) 表示传输层本次没有读到字节（部分串口驱动在超时时返回
// (0, nil)，因此这里不能依赖错误判断超时）；返回 (nil, ErrTimeout) 表示已过截止时间。
func (c *Client) readChunk(maxBytes int, deadline time.Time) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fail(ErrFrame, "no room left for more bytes")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, ErrTimeout
	}
	if err := c.t.SetReadTimeout(remaining); err != nil {
		return nil, err
	}
	buf := make([]byte, maxBytes)
	n, err := c.t.Read(buf)
	if err != nil {
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	if n == 0 {
		time.Sleep(zeroReadPause)
		return nil, nil
	}
	return buf[:n], nil
}

// readRTUFrame 先读 3 个字节判断响应类型，再读满整帧。
func (c *Client) readRTUFrame(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 0, MaxRTUFrameSize)
	for {
		if len(buf) >= 3 {
			total, err := rtuResponseLength(buf)
			if err != nil {
				return nil, err
			}
			if len(buf) >= total {
				return buf[:total], nil
			}
		}
		if len(buf) >= MaxRTUFrameSize {
			return nil, fail(ErrFrame, "no complete RTU frame within %d bytes", MaxRTUFrameSize)
		}
		chunk, err := c.readChunk(MaxRTUFrameSize-len(buf), deadline)
		if err != nil {
			if errors.Is(err, ErrTimeout) && len(buf) > 0 {
				return nil, fail(ErrTimeout, "incomplete RTU frame, got %d bytes", len(buf))
			}
			return nil, err
		}
		buf = append(buf, chunk...)
	}
}

// readASCIIFrame 丢弃帧外字符直到 ':'，再逐字节读到 CRLF。
func (c *Client) readASCIIFrame(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 0, MaxASCIIFrameSize)
	started := false
	for {
		if len(buf) >= MaxASCIIFrameSize {
			return nil, fail(ErrFrame, "no CRLF within %d bytes", MaxASCIIFrameSize)
		}
		chunk, err := c.readChunk(MaxASCIIFrameSize-len(buf), deadline)
		if err != nil {
			if errors.Is(err, ErrTimeout) && started {
				return nil, fail(ErrTimeout, "incomplete ASCII frame, got %d bytes", len(buf))
			}
			return nil, err
		}
		for _, b := range chunk {
			if !started {
				if b != asciiStart {
					continue
				}
				started = true
			}
			buf = append(buf, b)
			if len(buf) >= 2 && buf[len(buf)-2] == '\r' && buf[len(buf)-1] == '\n' {
				return buf, nil
			}
		}
	}
}

// ReadCoils 读取线圈（功能码 0x01）。
func (c *Client) ReadCoils(address, quantity uint16) ([]bool, error) {
	if err := checkQuantity(quantity, 1, MaxReadCoils); err != nil {
		return nil, err
	}
	if err := checkAddressRange(address, quantity); err != nil {
		return nil, err
	}
	pdu, err := c.transact(buildReadRequest(FuncReadCoils, address, quantity), true)
	if err != nil {
		return nil, err
	}
	return parseBitsResponse(pdu, FuncReadCoils, c.UnitID(), quantity)
}

// ReadDiscreteInputs 读取离散输入（功能码 0x02）。
func (c *Client) ReadDiscreteInputs(address, quantity uint16) ([]bool, error) {
	if err := checkQuantity(quantity, 1, MaxReadDiscreteInputs); err != nil {
		return nil, err
	}
	if err := checkAddressRange(address, quantity); err != nil {
		return nil, err
	}
	pdu, err := c.transact(buildReadRequest(FuncReadDiscreteInputs, address, quantity), true)
	if err != nil {
		return nil, err
	}
	return parseBitsResponse(pdu, FuncReadDiscreteInputs, c.UnitID(), quantity)
}

// ReadHoldingRegisters 读取保持寄存器（功能码 0x03）。
func (c *Client) ReadHoldingRegisters(address, quantity uint16) ([]uint16, error) {
	if err := checkQuantity(quantity, 1, MaxReadRegisters); err != nil {
		return nil, err
	}
	if err := checkAddressRange(address, quantity); err != nil {
		return nil, err
	}
	pdu, err := c.transact(buildReadRequest(FuncReadHoldingRegisters, address, quantity), true)
	if err != nil {
		return nil, err
	}
	return parseRegistersResponse(pdu, FuncReadHoldingRegisters, c.UnitID(), quantity)
}

// ReadInputRegisters 读取输入寄存器（功能码 0x04）。
func (c *Client) ReadInputRegisters(address, quantity uint16) ([]uint16, error) {
	if err := checkQuantity(quantity, 1, MaxReadRegisters); err != nil {
		return nil, err
	}
	if err := checkAddressRange(address, quantity); err != nil {
		return nil, err
	}
	pdu, err := c.transact(buildReadRequest(FuncReadInputRegisters, address, quantity), true)
	if err != nil {
		return nil, err
	}
	return parseRegistersResponse(pdu, FuncReadInputRegisters, c.UnitID(), quantity)
}

// WriteSingleCoil 写单个线圈（功能码 0x05）。
func (c *Client) WriteSingleCoil(address uint16, on bool) error {
	_, err := c.transact(buildWriteSingleCoilRequest(address, on), false)
	return err
}

// WriteSingleRegister 写单个寄存器（功能码 0x06）。
func (c *Client) WriteSingleRegister(address, value uint16) error {
	_, err := c.transact(buildWriteSingleRegisterRequest(address, value), false)
	return err
}

// WriteMultipleCoils 写多个线圈（功能码 0x0F）。
func (c *Client) WriteMultipleCoils(address uint16, values []bool) error {
	if err := checkQuantity(uint16(len(values)), 1, MaxWriteCoils); err != nil {
		return err
	}
	if err := checkAddressRange(address, uint16(len(values))); err != nil {
		return err
	}
	req := buildWriteMultipleCoilsRequest(address, values)
	_, err := c.transact(req, false)
	return err
}

// WriteMultipleRegisters 写多个寄存器（功能码 0x10）。
func (c *Client) WriteMultipleRegisters(address uint16, values []uint16) error {
	if err := checkQuantity(uint16(len(values)), 1, MaxWriteRegisters); err != nil {
		return err
	}
	if err := checkAddressRange(address, uint16(len(values))); err != nil {
		return err
	}
	req := buildWriteMultipleRegistersRequest(address, values)
	_, err := c.transact(req, false)
	return err
}

// MaskWriteRegister 用「与掩码」「或掩码」修改单个寄存器（功能码 0x16），
// 结果为 (当前值 AND andMask) OR (orMask AND (NOT andMask))。
func (c *Client) MaskWriteRegister(address, andMask, orMask uint16) error {
	_, err := c.transact(buildMaskWriteRegisterRequest(address, andMask, orMask), false)
	return err
}

// ReadWriteMultipleRegisters 在一次事务中先写后读（功能码 0x17）。
func (c *Client) ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress uint16, values []uint16) ([]uint16, error) {
	if err := checkQuantity(readQuantity, 1, MaxReadWriteReadRegisters); err != nil {
		return nil, err
	}
	if err := checkQuantity(uint16(len(values)), 1, MaxReadWriteWriteRegisters); err != nil {
		return nil, err
	}
	if err := checkAddressRange(readAddress, readQuantity); err != nil {
		return nil, err
	}
	if err := checkAddressRange(writeAddress, uint16(len(values))); err != nil {
		return nil, err
	}
	pdu, err := c.transact(
		buildReadWriteMultipleRegistersRequest(readAddress, readQuantity, writeAddress, values), true)
	if err != nil {
		return nil, err
	}
	return parseRegistersResponse(pdu, FuncReadWriteMultipleRegisters, c.UnitID(), readQuantity)
}
