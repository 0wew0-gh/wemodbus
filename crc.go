package wemodbus

// crcPoly 是 Modbus CRC16 使用的反向多项式。
const crcPoly = 0xA001

// crcTable 是 256 项 CRC16 查找表，在 init 中按 crcPoly 生成。
var crcTable [256]uint16

func init() {
	for i := range crcTable {
		crc := uint16(i)
		for bit := 0; bit < 8; bit++ {
			if crc&0x0001 != 0 {
				crc = crc>>1 ^ crcPoly
			} else {
				crc >>= 1
			}
		}
		crcTable[i] = crc
	}
}

// CRC16 计算 Modbus RTU 的 CRC16 校验值（初值 0xFFFF，多项式 0xA001）。
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = crc>>8 ^ crcTable[(crc^uint16(b))&0xFF]
	}
	return crc
}

// AppendCRC16 返回在 data 之后追加 CRC16 的新切片，线路上低字节在前。
// 传入的 data 不会被修改。
func AppendCRC16(data []byte) []byte {
	crc := CRC16(data)
	out := make([]byte, 0, len(data)+2)
	out = append(out, data...)
	return append(out, byte(crc), byte(crc>>8))
}

// CheckCRC16 校验以 CRC16 结尾的 RTU 帧（不含地址字段之前的任何内容）。
func CheckCRC16(frame []byte) error {
	if len(frame) < 3 {
		return fail(ErrFrame, "%d bytes is too short to carry a CRC", len(frame))
	}
	body := frame[:len(frame)-2]
	want := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	if got := CRC16(body); got != want {
		return fail(ErrCRC, "got 0x%04X, want 0x%04X", want, got)
	}
	return nil
}

// LRC 计算 ASCII 模式的纵向冗余校验值，即所有字节之和的二进制补码。
func LRC(data []byte) byte {
	var sum byte
	for _, b := range data {
		sum += b
	}
	// 用 int16 取负，避免 byte(-int8(sum)) 在 0x80 处溢出。
	return byte(-int16(sum))
}

// CheckLRC 校验以 LRC 结尾的 ASCII 帧体（从站地址到 PDU，不含起始符与 CRLF）。
func CheckLRC(frame []byte) error {
	if len(frame) < 2 {
		return fail(ErrFrame, "%d bytes is too short to carry an LRC", len(frame))
	}
	body := frame[:len(frame)-1]
	want := frame[len(frame)-1]
	if got := LRC(body); got != want {
		return fail(ErrLRC, "got 0x%02X, want 0x%02X", want, got)
	}
	return nil
}
