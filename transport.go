package wemodbus

import (
	"io"
	"time"

	"go.bug.st/serial"
)

// Parity 是串口校验位设置。
type Parity int

// 支持的校验位。
const (
	// ParityNone 关闭校验（默认）。
	ParityNone Parity = iota
	// ParityOdd 奇校验。
	ParityOdd
	// ParityEven 偶校验。
	ParityEven
)

// String 返回校验位名称。
func (p Parity) String() string {
	switch p {
	case ParityNone:
		return "none"
	case ParityOdd:
		return "odd"
	case ParityEven:
		return "even"
	default:
		return "unknown"
	}
}

// StopBits 是串口停止位设置。
type StopBits int

// 支持的停止位。
const (
	// StopBitsOne 1 位停止位（默认）。
	StopBitsOne StopBits = iota
	// StopBitsOnePointFive 1.5 位停止位。
	StopBitsOnePointFive
	// StopBitsTwo 2 位停止位。
	StopBitsTwo
)

// String 返回停止位名称。
func (s StopBits) String() string {
	switch s {
	case StopBitsOne:
		return "1"
	case StopBitsOnePointFive:
		return "1.5"
	case StopBitsTwo:
		return "2"
	default:
		return "unknown"
	}
}

// SerialConfig 是串口参数。BaudRate 与 DataBits 为零时分别取
// DefaultBaudRate 与 DefaultDataBits，Parity 与 StopBits 的零值即无校验、1 位停止位。
type SerialConfig struct {
	PortName string
	BaudRate int
	DataBits int
	Parity   Parity
	StopBits StopBits
}

// Transport 是 Modbus 报文的双向字节通道。除串口外也可用管道或内存实现，
// 便于测试。
//
// SetReadTimeout 的语义是「Read 最多阻塞 d 后返回」，超时返回 0 字节且不报错；
// 上层始终以自己维护的截止时间为准，不依赖传输层的超时错误。
type Transport interface {
	io.ReadWriteCloser
	SetReadTimeout(d time.Duration) error
}

// InputBufferResetter 由能够丢弃接收缓冲的 Transport 实现。每次事务开始前，
// 若 Transport 实现了该接口，Client 会先清空接收缓冲，避免上一次的残帧干扰。
type InputBufferResetter interface {
	ResetInputBuffer() error
}

// Ports 列出本机可用的串口名称。
func Ports() ([]string, error) {
	return serial.GetPortsList()
}

// OpenSerial 按 cfg 打开串口。cfg.PortName 为空时返回底层驱动的错误。
func OpenSerial(cfg SerialConfig) (Transport, error) {
	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		Parity:   serialParity(cfg.Parity),
		StopBits: serialStopBits(cfg.StopBits),
	}
	if mode.BaudRate <= 0 {
		mode.BaudRate = DefaultBaudRate
	}
	if mode.DataBits <= 0 {
		mode.DataBits = DefaultDataBits
	}
	port, err := serial.Open(cfg.PortName, mode)
	if err != nil {
		return nil, err
	}
	return &serialPort{port: port}, nil
}

func serialParity(p Parity) serial.Parity {
	switch p {
	case ParityOdd:
		return serial.OddParity
	case ParityEven:
		return serial.EvenParity
	default:
		return serial.NoParity
	}
}

func serialStopBits(s StopBits) serial.StopBits {
	switch s {
	case StopBitsOnePointFive:
		return serial.OnePointFiveStopBits
	case StopBitsTwo:
		return serial.TwoStopBits
	default:
		return serial.OneStopBit
	}
}

// serialPort 把 go.bug.st/serial 的 Port 适配为 Transport。
type serialPort struct {
	port serial.Port
}

func (s *serialPort) Read(p []byte) (int, error)  { return s.port.Read(p) }
func (s *serialPort) Write(p []byte) (int, error) { return s.port.Write(p) }
func (s *serialPort) Close() error                { return s.port.Close() }

func (s *serialPort) SetReadTimeout(d time.Duration) error {
	return s.port.SetReadTimeout(d)
}

func (s *serialPort) ResetInputBuffer() error { return s.port.ResetInputBuffer() }
