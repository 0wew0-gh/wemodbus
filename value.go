package wemodbus

import (
	"fmt"
	"math"
)

// ByteOrder 描述多寄存器数值的字节序。
//
// 四个字母代表一个 32 位数值的 4 个字节在报文中的排列顺序：A 是最高字节、
// D 是最低字节，一个寄存器承载两个字节。64 位数值（4 个寄存器）在此基础上
// 扩展：ABCD 即完整大端，CDAB 交换两个 32 位半部，BADC 在寄存器内部交换
// 字节，DCBA 则整体反序（等价于完整小端）。
type ByteOrder int

// 支持的字节序。
const (
	// ABCD 标准大端：寄存器 0 = AB，寄存器 1 = CD。
	ABCD ByteOrder = iota
	// CDAB 寄存器交换：寄存器 0 = CD，寄存器 1 = AB。
	CDAB
	// BADC 寄存器内字节交换：寄存器 0 = BA，寄存器 1 = DC。
	BADC
	// DCBA 整体反序：寄存器 0 = DC，寄存器 1 = BA。
	DCBA
)

// String 返回字节序名称。
func (o ByteOrder) String() string {
	switch o {
	case ABCD:
		return "ABCD"
	case CDAB:
		return "CDAB"
	case BADC:
		return "BADC"
	case DCBA:
		return "DCBA"
	default:
		return fmt.Sprintf("ByteOrder(%d)", int(o))
	}
}

// permuteBytes 把按大端排列的字节串（长度必须是 4 或 8）转换为 o 指定的
// 传输顺序。四种排列都是自逆的，因此解码时使用同一个函数。
func permuteBytes(b []byte, o ByteOrder) []byte {
	regs := len(b) / 2
	out := make([]byte, len(b))
	for i := 0; i < regs; i++ {
		hi, lo := b[2*i], b[2*i+1]
		j, swap := i, false
		switch o {
		case CDAB:
			j = (i + regs/2) % regs
		case BADC:
			swap = true
		case DCBA:
			j, swap = regs-1-i, true
		}
		if swap {
			hi, lo = lo, hi
		}
		out[2*j], out[2*j+1] = hi, lo
	}
	return out
}

// bigEndianBytes 返回 v 的大端字节表示，共 n 个字节。
func bigEndianBytes(v uint64, n int) []byte {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = byte(v >> (8 * uint(n-1-i)))
	}
	return b
}

// bytesToUint 把大端字节串还原为整数。
func bytesToUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// registersToBytes 把寄存器按大端展开为字节串。
func registersToBytes(r []uint16) []byte {
	b := make([]byte, 0, len(r)*2)
	for _, v := range r {
		b = append(b, byte(v>>8), byte(v))
	}
	return b
}

// bytesToRegisters 把字节串按每两个字节一组打包为寄存器。
func bytesToRegisters(b []byte) []uint16 {
	r := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		r = append(r, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return r
}

// Uint32ToRegisters 按 o 把 v 编码为 2 个寄存器。
func Uint32ToRegisters(v uint32, o ByteOrder) []uint16 {
	return bytesToRegisters(permuteBytes(bigEndianBytes(uint64(v), 4), o))
}

// RegistersToUint32 按 o 从 2 个寄存器解码。r 长度不足 2 时 panic。
func RegistersToUint32(r []uint16, o ByteOrder) uint32 {
	if len(r) < 2 {
		panic("wemodbus: RegistersToUint32 requires at least 2 registers")
	}
	return uint32(bytesToUint(permuteBytes(registersToBytes(r[:2]), o)))
}

// Int32ToRegisters 按 o 把 v 的补码编码为 2 个寄存器。
func Int32ToRegisters(v int32, o ByteOrder) []uint16 {
	return Uint32ToRegisters(uint32(v), o)
}

// RegistersToInt32 按 o 从 2 个寄存器解码补码。r 长度不足 2 时 panic。
func RegistersToInt32(r []uint16, o ByteOrder) int32 {
	return int32(RegistersToUint32(r, o))
}

// Float32ToRegisters 按 o 把 IEEE-754 单精度浮点数编码为 2 个寄存器。
func Float32ToRegisters(v float32, o ByteOrder) []uint16 {
	return Uint32ToRegisters(math.Float32bits(v), o)
}

// RegistersToFloat32 按 o 从 2 个寄存器解码单精度浮点数。r 长度不足 2 时 panic。
func RegistersToFloat32(r []uint16, o ByteOrder) float32 {
	return math.Float32frombits(RegistersToUint32(r, o))
}

// Uint64ToRegisters 按 o 把 v 编码为 4 个寄存器。
func Uint64ToRegisters(v uint64, o ByteOrder) []uint16 {
	return bytesToRegisters(permuteBytes(bigEndianBytes(v, 8), o))
}

// RegistersToUint64 按 o 从 4 个寄存器解码。r 长度不足 4 时 panic。
func RegistersToUint64(r []uint16, o ByteOrder) uint64 {
	if len(r) < 4 {
		panic("wemodbus: RegistersToUint64 requires at least 4 registers")
	}
	return bytesToUint(permuteBytes(registersToBytes(r[:4]), o))
}

// Int64ToRegisters 按 o 把 v 的补码编码为 4 个寄存器。
func Int64ToRegisters(v int64, o ByteOrder) []uint16 {
	return Uint64ToRegisters(uint64(v), o)
}

// RegistersToInt64 按 o 从 4 个寄存器解码补码。r 长度不足 4 时 panic。
func RegistersToInt64(r []uint16, o ByteOrder) int64 {
	return int64(RegistersToUint64(r, o))
}

// Float64ToRegisters 按 o 把 IEEE-754 双精度浮点数编码为 4 个寄存器。
func Float64ToRegisters(v float64, o ByteOrder) []uint16 {
	return Uint64ToRegisters(math.Float64bits(v), o)
}

// RegistersToFloat64 按 o 从 4 个寄存器解码双精度浮点数。r 长度不足 4 时 panic。
func RegistersToFloat64(r []uint16, o ByteOrder) float64 {
	return math.Float64frombits(RegistersToUint64(r, o))
}
