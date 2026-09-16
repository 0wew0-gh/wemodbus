package wemodbus

// Modbus TCP 报文 = MBAP 头 + PDU，没有 CRC / LRC，帧长由 MBAP 的长度域给出，
// 因此不存在 RTU 那样的帧分界问题。
//
// MBAP 头（7 字节）：
//
//	事务标识 2 字节：主站生成，从站必须在响应里原样回填，供主站匹配请求
//	协议标识 2 字节：Modbus 恒为 0
//	长度      2 字节：其后字节数，即单元标识 1 + PDU
//	单元标识  1 字节：从站地址
const (
	mbapHeaderSize = 7
	mbapProtocolID = 0
	// MaxTCPFrameSize 是 MBAP 头加最大 PDU 的长度。
	MaxTCPFrameSize = mbapHeaderSize + maxPDUSize
)

// BuildTCPFrame 把 PDU 封装成 Modbus TCP 报文（MBAP 头 + PDU）。
func BuildTCPFrame(transactionID uint16, unitID byte, pdu []byte) []byte {
	frame := make([]byte, 0, mbapHeaderSize+len(pdu))
	frame = append(frame,
		byte(transactionID>>8), byte(transactionID),
		byte(mbapProtocolID>>8), byte(mbapProtocolID),
		byte((len(pdu)+1)>>8), byte(len(pdu)+1),
		unitID)
	return append(frame, pdu...)
}

// ParseTCPFrame 解析 Modbus TCP 报文，返回事务标识、单元标识与 PDU。
func ParseTCPFrame(adu []byte) (transactionID uint16, unitID byte, pdu []byte, err error) {
	if len(adu) < mbapHeaderSize {
		return 0, 0, nil, fail(ErrFrame, "TCP frame of %d bytes is too short", len(adu))
	}
	transactionID = be16(adu[0], adu[1])
	if protocol := be16(adu[2], adu[3]); protocol != mbapProtocolID {
		return 0, 0, nil, fail(ErrFrame, "unexpected protocol id 0x%04X", protocol)
	}
	length := int(be16(adu[4], adu[5]))
	if length < 2 || mbapHeaderSize+length-1 != len(adu) {
		return 0, 0, nil, fail(ErrFrame, "MBAP declares %d bytes, got %d", length, len(adu)-mbapHeaderSize+1)
	}
	pdu = adu[mbapHeaderSize:]
	if len(pdu) > maxPDUSize {
		return 0, 0, nil, fail(ErrFrame, "PDU of %d bytes exceeds %d", len(pdu), maxPDUSize)
	}
	return transactionID, adu[6], pdu, nil
}

// tcpFrameLength 按 MBAP 的长度域推算整帧长度。
func tcpFrameLength(header []byte) (int, error) {
	if len(header) < mbapHeaderSize {
		return 0, fail(ErrFrame, "TCP header of %d bytes is too short", len(header))
	}
	length := int(be16(header[4], header[5]))
	if length < 2 {
		return 0, fail(ErrFrame, "MBAP declares %d bytes", length)
	}
	total := mbapHeaderSize + length - 1
	if total > MaxTCPFrameSize {
		return 0, fail(ErrFrame, "MBAP declares %d bytes", length)
	}
	return total, nil
}
