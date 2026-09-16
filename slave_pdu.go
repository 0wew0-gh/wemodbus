package wemodbus

// 本文件是 pdu.go 的镜像：把从站收到的请求 PDU 解析成参数，再把处理结果封装成
// 响应 PDU。两边的字节约定完全一致，都遵循 Modbus 的大端寄存器序与 LSB first
// 线圈打包。

// be16 把两个大端字节拼成 16 位整数。
func be16(hi, lo byte) uint16 {
	return uint16(hi)<<8 | uint16(lo)
}

// checkRequestPDU 校验请求 PDU 的功能码与固定长度。
func checkRequestPDU(pdu []byte, function byte, wantLen int) error {
	if len(pdu) == 0 {
		return fail(ErrFrame, "empty request PDU")
	}
	if pdu[0] != function {
		return fail(ErrFrame, "unexpected function code 0x%02X", pdu[0])
	}
	if len(pdu) != wantLen {
		return fail(ErrFrame, "request PDU is %d bytes, want %d", len(pdu), wantLen)
	}
	return nil
}

// parseReadRequest 解析读类请求（0x01 / 0x02 / 0x03 / 0x04）。
func parseReadRequest(pdu []byte, function byte) (address, quantity uint16, err error) {
	if err := checkRequestPDU(pdu, function, 5); err != nil {
		return 0, 0, err
	}
	return be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4]), nil
}

// parseWriteSingleCoilRequest 解析 0x05 请求；线圈值不是 0xFF00 / 0x0000 时返回
// 「非法数据值」异常，由从站回 0x03。
func parseWriteSingleCoilRequest(pdu []byte) (address uint16, on bool, err error) {
	if err := checkRequestPDU(pdu, FuncWriteSingleCoil, 5); err != nil {
		return 0, false, err
	}
	address = be16(pdu[1], pdu[2])
	switch value := be16(pdu[3], pdu[4]); value {
	case 0xFF00:
		return address, true, nil
	case 0x0000:
		return address, false, nil
	default:
		return 0, false, exceptionf(ExceptionIllegalDataValue,
			"coil value must be 0xFF00 or 0x0000, got 0x%04X", value)
	}
}

// parseWriteSingleRegisterRequest 解析 0x06 请求。
func parseWriteSingleRegisterRequest(pdu []byte) (address, value uint16, err error) {
	if err := checkRequestPDU(pdu, FuncWriteSingleRegister, 5); err != nil {
		return 0, 0, err
	}
	return be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4]), nil
}

// parseWriteMultipleCoilsRequest 解析 0x0F 请求，返回地址、数量与线圈值。
func parseWriteMultipleCoilsRequest(pdu []byte) (address, quantity uint16, values []bool, err error) {
	if len(pdu) < 6 || pdu[0] != FuncWriteMultipleCoils {
		return 0, 0, nil, fail(ErrFrame, "malformed request PDU for function 0x0F")
	}
	address, quantity = be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4])
	byteCount := int(pdu[5])
	if len(pdu) != 6+byteCount {
		return 0, 0, nil, fail(ErrFrame, "request declares %d data bytes, got %d", byteCount, len(pdu)-6)
	}
	if want := (int(quantity) + 7) / 8; want != byteCount {
		return 0, 0, nil, fail(ErrFrame, "quantity %d needs %d data bytes, got %d", quantity, want, byteCount)
	}
	if quantity == 0 {
		return 0, 0, nil, fail(ErrFrame, "quantity is zero")
	}
	return address, quantity, unpackCoils(pdu[6:], quantity), nil
}

// parseWriteMultipleRegistersRequest 解析 0x10 请求，返回地址、数量与寄存器值。
func parseWriteMultipleRegistersRequest(pdu []byte) (address, quantity uint16, values []uint16, err error) {
	if len(pdu) < 6 || pdu[0] != FuncWriteMultipleRegisters {
		return 0, 0, nil, fail(ErrFrame, "malformed request PDU for function 0x10")
	}
	address, quantity = be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4])
	byteCount := int(pdu[5])
	if len(pdu) != 6+byteCount {
		return 0, 0, nil, fail(ErrFrame, "request declares %d data bytes, got %d", byteCount, len(pdu)-6)
	}
	if int(quantity)*2 != byteCount {
		return 0, 0, nil, fail(ErrFrame, "quantity %d needs %d data bytes, got %d", quantity, 2*int(quantity), byteCount)
	}
	if quantity == 0 {
		return 0, 0, nil, fail(ErrFrame, "quantity is zero")
	}
	values = make([]uint16, quantity)
	for i := range values {
		values[i] = be16(pdu[6+2*i], pdu[7+2*i])
	}
	return address, quantity, values, nil
}

// buildRegistersResponse 构造读寄存器的响应 PDU：功能码 + 字节数 + 数据。
func buildRegistersResponse(function byte, values []uint16) []byte {
	pdu := make([]byte, 0, 2+2*len(values))
	pdu = append(pdu, function, byte(2*len(values)))
	for _, v := range values {
		pdu = append(pdu, byte(v>>8), byte(v))
	}
	return pdu
}

// buildBitsResponse 构造读位（线圈 / 离散输入）的响应 PDU。
func buildBitsResponse(function byte, values []bool) []byte {
	packed := packCoils(values)
	pdu := make([]byte, 0, 2+len(packed))
	pdu = append(pdu, function, byte(len(packed)))
	return append(pdu, packed...)
}

// parseMaskWriteRegisterRequest 解析 0x16 请求。
func parseMaskWriteRegisterRequest(pdu []byte) (address, andMask, orMask uint16, err error) {
	if err := checkRequestPDU(pdu, FuncMaskWriteRegister, 7); err != nil {
		return 0, 0, 0, err
	}
	return be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4]), be16(pdu[5], pdu[6]), nil
}

// parseReadWriteMultipleRegistersRequest 解析 0x17 请求：先写一段，再从读地址读一段。
func parseReadWriteMultipleRegistersRequest(pdu []byte) (readAddress, readQuantity, writeAddress uint16, values []uint16, err error) {
	if len(pdu) < 10 || pdu[0] != FuncReadWriteMultipleRegisters {
		return 0, 0, 0, nil, fail(ErrFrame, "malformed request PDU for function 0x17")
	}
	readAddress, readQuantity = be16(pdu[1], pdu[2]), be16(pdu[3], pdu[4])
	writeAddress, writeQuantity := be16(pdu[5], pdu[6]), be16(pdu[7], pdu[8])
	byteCount := int(pdu[9])
	if len(pdu) != 10+byteCount {
		return 0, 0, 0, nil, fail(ErrFrame, "request declares %d data bytes, got %d", byteCount, len(pdu)-10)
	}
	if want := 2 * int(writeQuantity); want != byteCount {
		return 0, 0, 0, nil, fail(ErrFrame, "quantity %d needs %d data bytes, got %d", writeQuantity, want, byteCount)
	}
	if readQuantity == 0 || writeQuantity == 0 {
		return 0, 0, 0, nil, fail(ErrFrame, "quantity is zero")
	}
	values = make([]uint16, writeQuantity)
	for i := range values {
		values[i] = be16(pdu[10+2*i], pdu[11+2*i])
	}
	return readAddress, readQuantity, writeAddress, values, nil
}

// buildWriteEchoResponse 构造写类响应：回显请求 PDU 的前 length 个字节
// （功能码 + 地址 + 数值或数量；0x16 还要带上「或掩码」，所以回显 7 字节）。
func buildWriteEchoResponse(pdu []byte, length int) []byte {
	echo := make([]byte, length)
	copy(echo, pdu[:min(length, len(pdu))])
	return echo
}

// buildExceptionResponse 构造异常响应 PDU：功能码最高位置 1，后跟异常码。
func buildExceptionResponse(function byte, code ExceptionCode) []byte {
	return []byte{function | 0x80, byte(code)}
}
