package wemodbus

import (
	"bytes"
)

// 本包支持的功能码。
const (
	FuncReadCoils                  byte = 0x01
	FuncReadDiscreteInputs         byte = 0x02
	FuncReadHoldingRegisters       byte = 0x03
	FuncReadInputRegisters         byte = 0x04
	FuncWriteSingleCoil            byte = 0x05
	FuncWriteSingleRegister        byte = 0x06
	FuncWriteMultipleCoils         byte = 0x0F
	FuncWriteMultipleRegisters     byte = 0x10
	FuncMaskWriteRegister          byte = 0x16
	FuncReadWriteMultipleRegisters byte = 0x17
)

// 协议规定的数量上限。
const (
	// MaxReadCoils 是一次 0x01 请求可读取的最大线圈数。
	MaxReadCoils = 2000
	// MaxReadDiscreteInputs 是一次 0x02 请求可读取的最大离散输入数。
	MaxReadDiscreteInputs = 2000
	// MaxReadRegisters 是一次 0x03 / 0x04 请求可读取的最大寄存器数。
	MaxReadRegisters = 125
	// MaxWriteCoils 是一次 0x0F 请求可写入的最大线圈数。
	MaxWriteCoils = 1968
	// MaxWriteRegisters 是一次 0x10 请求可写入的最大寄存器数。
	MaxWriteRegisters = 123
	// MaxReadWriteReadRegisters 是一次 0x17 请求可读取的最大寄存器数。
	MaxReadWriteReadRegisters = 125
	// MaxReadWriteWriteRegisters 是一次 0x17 请求可写入的最大寄存器数。
	MaxReadWriteWriteRegisters = 121
)

// maxPDUSize 是 PDU 的最大长度：功能码 1 + 数据 252。
const maxPDUSize = 253

// checkQuantity 校验数量是否落在协议允许的闭区间内。
func checkQuantity(quantity uint16, min, max int) error {
	if int(quantity) < min || int(quantity) > max {
		return fail(ErrQuantity, "%d not in [%d, %d]", quantity, min, max)
	}
	return nil
}

// checkAddressRange 校验「起始地址 + 数量」不越过 16 位地址空间。
func checkAddressRange(address, quantity uint16) error {
	if end := int(address) + int(quantity); end > 0x10000 {
		return fail(ErrQuantity, "address 0x%04X + quantity %d exceeds address space", address, quantity)
	}
	return nil
}

// buildReadRequest 构造 0x01 / 0x02 / 0x03 / 0x04 的请求 PDU。
func buildReadRequest(function byte, address, quantity uint16) []byte {
	return []byte{function, byte(address >> 8), byte(address), byte(quantity >> 8), byte(quantity)}
}

// buildWriteSingleCoilRequest 构造 0x05 的请求 PDU，闭合用 0xFF00、断开用 0x0000。
func buildWriteSingleCoilRequest(address uint16, on bool) []byte {
	var value uint16
	if on {
		value = 0xFF00
	}
	return []byte{FuncWriteSingleCoil, byte(address >> 8), byte(address), byte(value >> 8), byte(value)}
}

// buildWriteSingleRegisterRequest 构造 0x06 的请求 PDU。
func buildWriteSingleRegisterRequest(address, value uint16) []byte {
	return []byte{FuncWriteSingleRegister, byte(address >> 8), byte(address), byte(value >> 8), byte(value)}
}

// packCoils 把线圈按 LSB first 打包成字节。
func packCoils(values []bool) []byte {
	packed := make([]byte, (len(values)+7)/8)
	for i, v := range values {
		if v {
			packed[i/8] |= 1 << (uint(i) % 8)
		}
	}
	return packed
}

// unpackCoils 把 LSB first 打包的字节还原为指定数量的线圈。
func unpackCoils(data []byte, quantity uint16) []bool {
	values := make([]bool, quantity)
	for i := range values {
		values[i] = data[i/8]&(1<<(uint(i)%8)) != 0
	}
	return values
}

// buildWriteMultipleCoilsRequest 构造 0x0F 的请求 PDU。
func buildWriteMultipleCoilsRequest(address uint16, values []bool) []byte {
	packed := packCoils(values)
	pdu := make([]byte, 0, 6+len(packed))
	pdu = append(pdu,
		FuncWriteMultipleCoils,
		byte(address>>8), byte(address),
		byte(len(values)>>8), byte(len(values)),
		byte(len(packed)))
	return append(pdu, packed...)
}

// buildWriteMultipleRegistersRequest 构造 0x10 的请求 PDU。
func buildWriteMultipleRegistersRequest(address uint16, values []uint16) []byte {
	pdu := make([]byte, 0, 6+2*len(values))
	pdu = append(pdu,
		FuncWriteMultipleRegisters,
		byte(address>>8), byte(address),
		byte(len(values)>>8), byte(len(values)),
		byte(2*len(values)))
	for _, v := range values {
		pdu = append(pdu, byte(v>>8), byte(v))
	}
	return pdu
}

// buildMaskWriteRegisterRequest 构造 0x16 的请求 PDU。
func buildMaskWriteRegisterRequest(address, andMask, orMask uint16) []byte {
	return []byte{
		FuncMaskWriteRegister,
		byte(address >> 8), byte(address),
		byte(andMask >> 8), byte(andMask),
		byte(orMask >> 8), byte(orMask),
	}
}

// buildReadWriteMultipleRegistersRequest 构造 0x17 的请求 PDU。
func buildReadWriteMultipleRegistersRequest(readAddress, readQuantity, writeAddress uint16, values []uint16) []byte {
	pdu := make([]byte, 0, 11+2*len(values))
	pdu = append(pdu,
		FuncReadWriteMultipleRegisters,
		byte(readAddress>>8), byte(readAddress),
		byte(readQuantity>>8), byte(readQuantity),
		byte(writeAddress>>8), byte(writeAddress),
		byte(len(values)>>8), byte(len(values)),
		byte(2*len(values)))
	for _, v := range values {
		pdu = append(pdu, byte(v>>8), byte(v))
	}
	return pdu
}

// checkFunction 校验响应 PDU 的功能码，并把从站异常响应转换为 *ExceptionError。
func checkFunction(pdu []byte, function byte, unitID byte) error {
	if len(pdu) == 0 {
		return fail(ErrFrame, "empty response PDU")
	}
	if pdu[0] == function {
		return nil
	}
	if pdu[0] == function|0x80 {
		if len(pdu) < 2 {
			return fail(ErrFrame, "truncated exception response")
		}
		return &ExceptionError{UnitID: unitID, Function: function, Code: ExceptionCode(pdu[1])}
	}
	return fail(ErrFunction, "got 0x%02X, want 0x%02X", pdu[0], function)
}

// parseReadResponse 校验读类响应（0x01 / 0x02 / 0x03 / 0x04 / 0x17）的
// 字节数声明，并返回数据区。
func parseReadResponse(pdu []byte, function byte, unitID byte, wantBytes int) ([]byte, error) {
	if err := checkFunction(pdu, function, unitID); err != nil {
		return nil, err
	}
	if len(pdu) < 2 {
		return nil, fail(ErrFrame, "truncated read response")
	}
	if declared := int(pdu[1]); declared != wantBytes {
		return nil, fail(ErrFrame, "response declares %d data bytes, want %d", declared, wantBytes)
	}
	data := pdu[2:]
	if len(data) != wantBytes {
		return nil, fail(ErrFrame, "response carries %d data bytes, want %d", len(data), wantBytes)
	}
	return data, nil
}

// parseBitsResponse 解析 0x01 / 0x02 的响应。
func parseBitsResponse(pdu []byte, function byte, unitID byte, quantity uint16) ([]bool, error) {
	data, err := parseReadResponse(pdu, function, unitID, int(quantity+7)/8)
	if err != nil {
		return nil, err
	}
	return unpackCoils(data, quantity), nil
}

// parseRegistersResponse 解析 0x03 / 0x04 / 0x17 的响应。
func parseRegistersResponse(pdu []byte, function byte, unitID byte, quantity uint16) ([]uint16, error) {
	data, err := parseReadResponse(pdu, function, unitID, 2*int(quantity))
	if err != nil {
		return nil, err
	}
	registers := make([]uint16, quantity)
	for i := range registers {
		registers[i] = uint16(data[2*i])<<8 | uint16(data[2*i+1])
	}
	return registers, nil
}

// isWriteFunction 判断功能码是否属于写类（响应必须回显请求）。
func isWriteFunction(function byte) bool {
	switch function {
	case FuncWriteSingleCoil, FuncWriteSingleRegister, FuncWriteMultipleCoils,
		FuncWriteMultipleRegisters, FuncMaskWriteRegister:
		return true
	default:
		return false
	}
}

// echoLength 返回写类功能的响应中需要回显的字节数（不含功能码本身）。
// 0x05 / 0x06 回显地址与数值，0x0F / 0x10 回显地址与数量，都是 4 字节；
// 0x16 额外回显「或掩码」，共 6 字节。
func echoLength(function byte) int {
	if function == FuncMaskWriteRegister {
		return 6
	}
	return 4
}

// parseEchoResponse 校验写类响应（0x05 / 0x06 / 0x0F / 0x10 / 0x16）对请求的回显。
// want 是期望回显的字节，不含功能码。
func parseEchoResponse(pdu []byte, function byte, unitID byte, want []byte) error {
	if err := checkFunction(pdu, function, unitID); err != nil {
		return err
	}
	if len(pdu) != len(want)+1 {
		return fail(ErrFrame, "echo response is %d bytes, want %d", len(pdu), len(want)+1)
	}
	if !bytes.Equal(pdu[1:], want) {
		return fail(ErrFrame, "echo mismatch: got % X, want % X", pdu[1:], want)
	}
	return nil
}
