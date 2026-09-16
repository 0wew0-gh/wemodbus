package wemodbus

import (
	"errors"
	"sync"
	"time"
)

// ServerConfig 是从站参数。零值即 RTU 模式、500ms 帧接收超时、应答任意从站地址。
type ServerConfig struct {
	// UnitID 是本从站的地址；为 0 时应答任意地址（含广播）。
	UnitID byte
	// Mode 是传输模式，零值为 ModeRTU。
	Mode Mode
	// Timeout 是等待一帧的时长，零值取 DefaultTimeout。
	Timeout time.Duration
}

// ServerStats 是从站的累计统计。
type ServerStats struct {
	Requests   uint64 // 收到的合法请求数
	Responses  uint64 // 回出的正常响应数
	Exceptions uint64 // 回出的异常响应数
	Ignored    uint64 // 忽略的帧：地址不符、CRC 错、请求格式非法等
}

// Server 是 Modbus 从站：从 Transport 读请求，交给 Handler 处理，再把响应写回。
//
//	model := wemodbus.NewDataModel(64, 64, 128, 64)
//	port, err := wemodbus.OpenSerial(wemodbus.SerialConfig{PortName: "COM3", BaudRate: 9600, DataBits: 8})
//	if err != nil {
//		log.Fatal(err)
//	}
//	server := wemodbus.NewServer(port, wemodbus.ServerConfig{UnitID: 1}, model)
//	defer server.Close()
//	log.Fatal(server.Serve())
//
// 广播（地址 0）的请求会被执行但不应答；地址不符的帧直接忽略。
type Server struct {
	mu      sync.Mutex
	t       Transport
	cfg     ServerConfig
	handler Handler
	closed  bool
	stats   ServerStats
}

// NewServer 用给定的 Transport 与处理器建立从站。
func NewServer(t Transport, cfg ServerConfig, h Handler) *Server {
	if t == nil {
		panic("wemodbus: nil transport")
	}
	if h == nil {
		panic("wemodbus: nil handler")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Server{t: t, cfg: cfg, handler: h}
}

// Config 返回从站配置。
func (s *Server) Config() ServerConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Serve 阻塞地处理请求，直到 Close 或传输层出错。Close 之后返回 nil。
func (s *Server) Serve() error {
	reader := &frameReader{
		t:         s.t,
		mode:      s.cfg.Mode,
		timeout:   s.cfg.Timeout,
		minPrefix: 2,
		length:    rtuRequestLength,
		verify:    CheckCRC16,
	}
	for {
		if s.isClosed() {
			return nil
		}
		frame, err := reader.read()
		if err != nil {
			if errors.Is(err, ErrTimeout) {
				continue // 总线空闲，继续等下一帧
			}
			if s.isClosed() {
				return nil
			}
			s.countIgnored()
			continue
		}
		s.serveFrame(frame)
	}
}

// Close 关闭从站与底层 Transport；Serve 会在下一次读取时返回。
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.t.Close()
}

// Stats 返回累计统计。
func (s *Server) Stats() ServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// serveFrame 处理一个完整的请求帧。
func (s *Server) serveFrame(frame []byte) {
	var (
		unitID byte
		pdu    []byte
		tid    uint16
		err    error
	)
	if s.cfg.Mode == ModeTCP {
		tid, unitID, pdu, err = ParseTCPFrame(frame)
	} else {
		unitID, pdu, err = ParseFrame(s.cfg.Mode, frame)
	}
	if err != nil {
		s.countIgnored()
		return
	}
	if !s.acceptUnit(unitID) {
		s.countIgnored()
		return
	}
	s.countRequest()

	response := s.handle(pdu)
	if response == nil {
		s.countIgnored()
		return
	}
	if unitID == 0 {
		return // 广播：执行但不应答
	}
	var out []byte
	if s.cfg.Mode == ModeTCP {
		out = BuildTCPFrame(tid, unitID, response) // 原样回填事务标识
	} else {
		out = BuildFrame(s.cfg.Mode, unitID, response)
	}
	if _, err := s.t.Write(out); err != nil {
		s.countIgnored()
		return
	}
	if response[0]&0x80 != 0 {
		s.countException()
		return
	}
	s.countResponse()
}

// acceptUnit 判断该请求是否属于本从站：广播与「应答任意地址」都接受。
func (s *Server) acceptUnit(unitID byte) bool {
	return unitID == 0 || s.cfg.UnitID == 0 || unitID == s.cfg.UnitID
}

// handle 处理请求 PDU 并返回响应 PDU；返回 nil 表示不应答（请求格式非法）。
func (s *Server) handle(pdu []byte) []byte {
	if len(pdu) == 0 {
		return nil
	}
	function := pdu[0]
	switch function {
	case FuncReadCoils:
		return s.readBits(function, pdu, s.handler.ReadCoils, MaxReadCoils)
	case FuncReadDiscreteInputs:
		return s.readBits(function, pdu, s.handler.ReadDiscreteInputs, MaxReadDiscreteInputs)
	case FuncReadHoldingRegisters:
		return s.readRegisters(function, pdu, s.handler.ReadHoldingRegisters, MaxReadRegisters)
	case FuncReadInputRegisters:
		return s.readRegisters(function, pdu, s.handler.ReadInputRegisters, MaxReadRegisters)
	case FuncWriteSingleCoil:
		return s.writeSingleCoil(pdu)
	case FuncWriteSingleRegister:
		return s.writeSingleRegister(pdu)
	case FuncWriteMultipleCoils:
		return s.writeMultipleCoils(pdu)
	case FuncWriteMultipleRegisters:
		return s.writeMultipleRegisters(pdu)
	case FuncMaskWriteRegister:
		return s.maskWriteRegister(pdu)
	case FuncReadWriteMultipleRegisters:
		return s.readWriteMultipleRegisters(pdu)
	default:
		return buildExceptionResponse(function, ExceptionIllegalFunction)
	}
}

func (s *Server) readBits(function byte, pdu []byte, read func(uint16, uint16) ([]bool, error), limit int) []byte {
	address, quantity, err := parseReadRequest(pdu, function)
	if err != nil {
		return s.parseFailure(function, err)
	}
	if err := checkQuantity(quantity, 1, limit); err != nil {
		return buildExceptionResponse(function, ExceptionIllegalDataValue)
	}
	values, err := read(address, quantity)
	if err != nil {
		return buildExceptionResponse(function, exceptionCodeOf(err))
	}
	return buildBitsResponse(function, values)
}

func (s *Server) readRegisters(function byte, pdu []byte, read func(uint16, uint16) ([]uint16, error), limit int) []byte {
	address, quantity, err := parseReadRequest(pdu, function)
	if err != nil {
		return s.parseFailure(function, err)
	}
	if err := checkQuantity(quantity, 1, limit); err != nil {
		return buildExceptionResponse(function, ExceptionIllegalDataValue)
	}
	values, err := read(address, quantity)
	if err != nil {
		return buildExceptionResponse(function, exceptionCodeOf(err))
	}
	return buildRegistersResponse(function, values)
}

func (s *Server) writeSingleCoil(pdu []byte) []byte {
	address, on, err := parseWriteSingleCoilRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncWriteSingleCoil, err)
	}
	if err := s.handler.WriteSingleCoil(address, on); err != nil {
		return buildExceptionResponse(FuncWriteSingleCoil, exceptionCodeOf(err))
	}
	return buildWriteEchoResponse(pdu, 5)
}

func (s *Server) writeSingleRegister(pdu []byte) []byte {
	address, value, err := parseWriteSingleRegisterRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncWriteSingleRegister, err)
	}
	if err := s.handler.WriteSingleRegister(address, value); err != nil {
		return buildExceptionResponse(FuncWriteSingleRegister, exceptionCodeOf(err))
	}
	return buildWriteEchoResponse(pdu, 5)
}

func (s *Server) writeMultipleCoils(pdu []byte) []byte {
	address, quantity, values, err := parseWriteMultipleCoilsRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncWriteMultipleCoils, err)
	}
	if err := checkQuantity(quantity, 1, MaxWriteCoils); err != nil {
		return buildExceptionResponse(FuncWriteMultipleCoils, ExceptionIllegalDataValue)
	}
	if err := s.handler.WriteMultipleCoils(address, values); err != nil {
		return buildExceptionResponse(FuncWriteMultipleCoils, exceptionCodeOf(err))
	}
	return buildWriteEchoResponse(pdu, 5)
}

func (s *Server) writeMultipleRegisters(pdu []byte) []byte {
	address, quantity, values, err := parseWriteMultipleRegistersRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncWriteMultipleRegisters, err)
	}
	if err := checkQuantity(quantity, 1, MaxWriteRegisters); err != nil {
		return buildExceptionResponse(FuncWriteMultipleRegisters, ExceptionIllegalDataValue)
	}
	if err := s.handler.WriteMultipleRegisters(address, values); err != nil {
		return buildExceptionResponse(FuncWriteMultipleRegisters, exceptionCodeOf(err))
	}
	return buildWriteEchoResponse(pdu, 5)
}

// maskWriteRegister 处理 0x16：处理器没实现 MaskWriteHandler 时回非法功能码。
func (s *Server) maskWriteRegister(pdu []byte) []byte {
	address, andMask, orMask, err := parseMaskWriteRegisterRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncMaskWriteRegister, err)
	}
	handler, ok := s.handler.(MaskWriteHandler)
	if !ok {
		return buildExceptionResponse(FuncMaskWriteRegister, ExceptionIllegalFunction)
	}
	if err := handler.MaskWriteRegister(address, andMask, orMask); err != nil {
		return buildExceptionResponse(FuncMaskWriteRegister, exceptionCodeOf(err))
	}
	// 0x16 的响应完整回显请求：功能码 + 地址 + 与掩码 + 或掩码。
	return buildWriteEchoResponse(pdu, 7)
}

// readWriteMultipleRegisters 处理 0x17：先写一段，再把读地址的内容回给主站。
func (s *Server) readWriteMultipleRegisters(pdu []byte) []byte {
	readAddress, readQuantity, writeAddress, values, err := parseReadWriteMultipleRegistersRequest(pdu)
	if err != nil {
		return s.parseFailure(FuncReadWriteMultipleRegisters, err)
	}
	handler, ok := s.handler.(ReadWriteHandler)
	if !ok {
		return buildExceptionResponse(FuncReadWriteMultipleRegisters, ExceptionIllegalFunction)
	}
	if err := checkQuantity(readQuantity, 1, MaxReadWriteReadRegisters); err != nil {
		return buildExceptionResponse(FuncReadWriteMultipleRegisters, ExceptionIllegalDataValue)
	}
	if err := checkQuantity(uint16(len(values)), 1, MaxReadWriteWriteRegisters); err != nil {
		return buildExceptionResponse(FuncReadWriteMultipleRegisters, ExceptionIllegalDataValue)
	}
	result, err := handler.ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress, values)
	if err != nil {
		return buildExceptionResponse(FuncReadWriteMultipleRegisters, exceptionCodeOf(err))
	}
	return buildRegistersResponse(FuncReadWriteMultipleRegisters, result)
}

// parseFailure 处理请求解析失败：解析时已给出异常码的回异常响应，否则忽略该帧
// （多为帧错位或结构非法，按规范不应答）。
func (s *Server) parseFailure(function byte, err error) []byte {
	var ex *ExceptionError
	if errors.As(err, &ex) && ex.Code != 0 {
		return buildExceptionResponse(function, ex.Code)
	}
	return nil
}

// exceptionCodeOf 把处理器的错误映射成从站异常码：*ExceptionError 用它自己的码，
// 其它错误按 0x04「从站设备故障」处理。
func exceptionCodeOf(err error) ExceptionCode {
	var ex *ExceptionError
	if errors.As(err, &ex) && ex.Code != 0 {
		return ex.Code
	}
	return ExceptionSlaveDeviceFailure
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) countRequest() {
	s.mu.Lock()
	s.stats.Requests++
	s.mu.Unlock()
}

func (s *Server) countResponse() {
	s.mu.Lock()
	s.stats.Responses++
	s.mu.Unlock()
}

func (s *Server) countException() {
	s.mu.Lock()
	s.stats.Exceptions++
	s.mu.Unlock()
}

func (s *Server) countIgnored() {
	s.mu.Lock()
	s.stats.Ignored++
	s.mu.Unlock()
}
