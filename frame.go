package wemodbus

import (
	"bytes"
	"encoding/hex"
	"fmt"
)

// Mode 是 Modbus 的传输模式。
type Mode int

const (
	// ModeRTU 是二进制 RTU 模式，帧之间以至少 3.5 个字符时间的静默分隔，CRC16 校验。
	ModeRTU Mode = iota
	// ModeASCII 是十六进制 ASCII 模式，帧以 ':' 开始、以 CRLF 结束，LRC 校验。
	ModeASCII
	// ModeTCP 是 Modbus TCP 模式，报文由 MBAP 头定界，没有 CRC / LRC。
	ModeTCP
)

// String 返回模式名称。
func (m Mode) String() string {
	switch m {
	case ModeRTU:
		return "RTU"
	case ModeASCII:
		return "ASCII"
	case ModeTCP:
		return "TCP"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

const (
	// asciiStart 是 ASCII 帧的起始符。
	asciiStart byte = ':'
	// asciiEnd 是 ASCII 帧的结束符。
	asciiEnd = "\r\n"

	// MaxRTUFrameSize 是 RTU ADU 的最大长度：地址 1 + PDU 253 + CRC 2。
	MaxRTUFrameSize = 256
	// MaxASCIIFrameSize 是 ASCII ADU 的最大长度：':' 1 + 510 个十六进制字符 + LRC 2 + CRLF 2。
	MaxASCIIFrameSize = 515
)

const hexDigits = "0123456789ABCDEF"

// appendHex 把 src 以大写十六进制追加到 dst。
func appendHex(dst, src []byte) []byte {
	for _, b := range src {
		dst = append(dst, hexDigits[b>>4], hexDigits[b&0x0F])
	}
	return dst
}

// BuildFrame 把 PDU 封装成完整的 ADU。
//
// RTU：地址 + PDU + CRC16（低字节在前）。
// ASCII：':' + 地址与 PDU 的十六进制文本 + LRC 的十六进制文本 + CRLF。
func BuildFrame(mode Mode, unitID byte, pdu []byte) []byte {
	if mode == ModeASCII {
		body := make([]byte, 0, len(pdu)+1)
		body = append(body, unitID)
		body = append(body, pdu...)
		out := make([]byte, 0, 2*(len(body)+1)+3)
		out = append(out, asciiStart)
		out = appendHex(out, body)
		out = appendHex(out, []byte{LRC(body)})
		return append(out, asciiEnd...)
	}
	out := make([]byte, 0, len(pdu)+3)
	out = append(out, unitID)
	out = append(out, pdu...)
	return AppendCRC16(out)
}

// ParseFrame 校验并拆解一个完整的 ADU，返回从站地址与 PDU。
func ParseFrame(mode Mode, adu []byte) (byte, []byte, error) {
	var (
		unitID byte
		pdu    []byte
		err    error
	)
	if mode == ModeASCII {
		unitID, pdu, err = decodeASCIIFrame(adu)
	} else {
		unitID, pdu, err = decodeRTUFrame(adu)
	}
	if err != nil {
		return 0, nil, err
	}
	if len(pdu) > maxPDUSize {
		return 0, nil, fail(ErrFrame, "PDU of %d bytes exceeds %d", len(pdu), maxPDUSize)
	}
	return unitID, pdu, nil
}

// decodeRTUFrame 校验 CRC 并拆出地址与 PDU。
func decodeRTUFrame(adu []byte) (byte, []byte, error) {
	if len(adu) < 4 {
		return 0, nil, fail(ErrFrame, "RTU frame of %d bytes is too short", len(adu))
	}
	if len(adu) > MaxRTUFrameSize {
		return 0, nil, fail(ErrFrame, "RTU frame of %d bytes exceeds %d", len(adu), MaxRTUFrameSize)
	}
	if err := CheckCRC16(adu); err != nil {
		return 0, nil, err
	}
	return adu[0], adu[1 : len(adu)-2], nil
}

// decodeASCIIFrame 校验 LRC 并拆出地址与 PDU。
func decodeASCIIFrame(adu []byte) (byte, []byte, error) {
	if len(adu) > MaxASCIIFrameSize {
		return 0, nil, fail(ErrFrame, "ASCII frame of %d bytes exceeds %d", len(adu), MaxASCIIFrameSize)
	}
	if len(adu) == 0 || adu[0] != asciiStart {
		return 0, nil, fail(ErrFrame, "ASCII frame must start with %q", asciiStart)
	}
	body := adu[1:]
	if !bytes.HasSuffix(body, []byte(asciiEnd)) {
		return 0, nil, fail(ErrFrame, "ASCII frame must end with CRLF")
	}
	body = body[:len(body)-len(asciiEnd)]
	if len(body) < 6 || len(body)%2 != 0 {
		return 0, nil, fail(ErrFrame, "ASCII frame body must be at least 3 bytes of hex digits in pairs")
	}
	raw := make([]byte, hex.DecodedLen(len(body)))
	n, err := hex.Decode(raw, body)
	if err != nil {
		return 0, nil, fail(ErrFrame, "%v", err)
	}
	raw = raw[:n]
	if err := CheckLRC(raw); err != nil {
		return 0, nil, err
	}
	return raw[0], raw[1 : len(raw)-1], nil
}

// rtuResponseLength 根据已读到的帧头推算 RTU 响应的总长度。
//
// header 至少要有 3 个字节（地址、功能码、第三个字节）。异常响应的功能码
// 最高位为 1，总长固定 5；读类响应为「3 + 字节数 + 2」；0x05 / 0x06 / 0x0F /
// 0x10 的响应是「地址 + 数值或数量」，总长固定 8；0x16 多回显一个「或掩码」，
// 总长 10。
func rtuResponseLength(header []byte) (int, error) {
	if len(header) < 3 {
		return 0, fail(ErrFrame, "RTU response header of %d bytes is too short", len(header))
	}
	function := header[1]
	if function&0x80 != 0 {
		return 5, nil
	}
	switch function {
	case FuncReadCoils, FuncReadDiscreteInputs, FuncReadHoldingRegisters,
		FuncReadInputRegisters, FuncReadWriteMultipleRegisters:
		total := int(header[2]) + 5
		if total > MaxRTUFrameSize {
			return 0, fail(ErrFrame, "RTU response declares %d data bytes", header[2])
		}
		return total, nil
	case FuncMaskWriteRegister:
		return 10, nil
	case FuncWriteSingleCoil, FuncWriteSingleRegister, FuncWriteMultipleCoils,
		FuncWriteMultipleRegisters:
		return 8, nil
	default:
		return 0, fail(ErrFrame, "unsupported function code 0x%02X", function)
	}
}

// rtuRequestLength 根据已读到的帧头推算 RTU 请求的总长度（从站接收用）。
//
// 读类与单写类请求固定 8 字节，0x16 为 10 字节；0x0F / 0x10 / 0x17 带字节数域，
// 需要在读到第 7（或第 11）个字节后才能算出总长。遇到不支持的功能码时按最小的
// 请求长度 8 字节估算，以便校验通过后回「非法功能码」异常响应；无法确定长度的
// 请求会由帧读取层的重同步逻辑兜底。
func rtuRequestLength(frame []byte) (int, error) {
	if len(frame) < 2 {
		return 0, fail(ErrFrame, "RTU request of %d bytes is too short", len(frame))
	}
	switch frame[1] {
	case FuncReadCoils, FuncReadDiscreteInputs, FuncReadHoldingRegisters,
		FuncReadInputRegisters, FuncWriteSingleCoil, FuncWriteSingleRegister:
		return 8, nil
	case FuncMaskWriteRegister:
		return 10, nil
	case FuncWriteMultipleCoils, FuncWriteMultipleRegisters:
		// 地址 1 + 功能码 1 + 地址 2 + 数量 2 + 字节数 1 + 数据 N + CRC 2
		if len(frame) < 7 {
			return 0, fail(ErrFrame, "RTU request of %d bytes is too short", len(frame))
		}
		total := 9 + int(frame[6])
		if total > MaxRTUFrameSize {
			return 0, fail(ErrFrame, "RTU request declares %d data bytes", frame[6])
		}
		return total, nil
	case FuncReadWriteMultipleRegisters:
		// 地址 1 + 功能码 1 + 读地址 2 + 读数量 2 + 写地址 2 + 写数量 2 + 字节数 1 + 数据 N + CRC 2
		if len(frame) < 11 {
			return 0, fail(ErrFrame, "RTU request of %d bytes is too short", len(frame))
		}
		total := 13 + int(frame[10])
		if total > MaxRTUFrameSize {
			return 0, fail(ErrFrame, "RTU request declares %d data bytes", frame[10])
		}
		return total, nil
	default:
		return 8, nil
	}
}
