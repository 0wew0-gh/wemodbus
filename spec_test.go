package wemodbus

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// registerReply 构造一个读寄存器响应帧，数据区是给定的寄存器值。
func registerReply(unitID, function byte, values ...uint16) []byte {
	data := []byte{byte(2 * len(values))}
	for _, v := range values {
		data = append(data, byte(v>>8), byte(v))
	}
	return rtuReplyFunc(unitID, function, data...)
}

func TestParseSpec(t *testing.T) {
	entries, err := parseSpec("1004:float32, 1006:float32")
	if err != nil {
		t.Fatalf("parseSpec: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("解析出 %d 项，want 2", len(entries))
	}
	if entries[0].address != 1004 || entries[0].length != 2 || entries[0].kind != "float32" || entries[0].index != 0 {
		t.Fatalf("第 1 项 = %+v", entries[0])
	}
	if entries[1].address != 1006 || entries[1].index != 1 {
		t.Fatalf("第 2 项 = %+v", entries[1])
	}
}

func TestParseSpecAcceptsVariants(t *testing.T) {
	tests := []struct {
		spec  string
		kind  string
		count uint16
	}{
		{"0:uint16", "uint16", 1},
		{"0:int16", "int16", 1},
		{"0:uint32", "uint32", 2},
		{"0:int32", "int32", 2},
		{"0:float64", "float64", 4},
		{"0:uint64", "uint64", 4},
		{"0:int64", "int64", 4},
		{"0x10:FLOAT32", "float32", 2},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			entries, err := parseSpec(tt.spec)
			if err != nil {
				t.Fatalf("parseSpec(%q): %v", tt.spec, err)
			}
			if entries[0].kind != tt.kind || entries[0].length != tt.count {
				t.Fatalf("parseSpec(%q) = %+v", tt.spec, entries[0])
			}
		})
	}
}

func TestParseSpecErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"空清单", ""},
		{"只有空白", "   "},
		{"空项", ",1004:float32"},
		{"缺段", "1004"},
		{"缺类型", "1004:"},
		{"段太多", "1004:2:float32"},
		{"地址非法", "abc:float32"},
		{"地址超范围", "70000:float32"},
		{"地址为负", "-1:float32"},
		{"类型不支持", "1004:float16"},
		{"类型写成数字", "1004:2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseSpec(tt.spec); !errors.Is(err, ErrSpec) {
				t.Fatalf("parseSpec(%q) = %v, want ErrSpec", tt.spec, err)
			}
		})
	}
}

func TestReadValuesMergesAdjacentEntries(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, 0x3FC0, 0x0000, 0xC010, 0x0000))

	values, err := c.ReadValues("1004:float32,1006:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	if len(values) != 2 || values[0] != 1.5 || values[1] != -2.25 {
		t.Fatalf("ReadValues = %v, want [1.5 -2.25]", values)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("首尾相接的两项应合并为一次请求，实际发了 %d 帧", mt.writeCount())
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x03, 0x03, 0xEC, 0x00, 0x04}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestReadValuesKeepsSpecOrder(t *testing.T) {
	// 清单里 1006 在前、1004 在后，合并读取后返回值仍要按清单顺序。
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, 0x3FC0, 0x0000, 0xC010, 0x0000))

	values, err := c.ReadValues("1006:float32,1004:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	if len(values) != 2 || values[0] != -2.25 || values[1] != 1.5 {
		t.Fatalf("ReadValues = %v, want [-2.25 1.5]", values)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("发送了 %d 帧, want 1", mt.writeCount())
	}
}

func TestReadValuesNonAdjacentEntries(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, 0x3FC0, 0x0000),
		registerReply(0x01, FuncReadHoldingRegisters, 0xC010, 0x0000))

	values, err := c.ReadValues("1004:float32,1010:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	if len(values) != 2 || values[0] != 1.5 || values[1] != -2.25 {
		t.Fatalf("ReadValues = %v, want [1.5 -2.25]", values)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("不连续的两项应发两帧，实际发了 %d 帧", mt.writeCount())
	}
	first := mt.frame(0)
	if want := []byte{0x01, 0x03, 0x03, 0xEC, 0x00, 0x02}; !bytes.Equal(first[:len(first)-2], want) {
		t.Fatalf("第 1 帧 = % X, want % X", first, want)
	}
	second := mt.frame(1)
	if want := []byte{0x01, 0x03, 0x03, 0xF2, 0x00, 0x02}; !bytes.Equal(second[:len(second)-2], want) {
		t.Fatalf("第 2 帧 = % X, want % X", second, want)
	}
}

func TestReadInputValuesUsesFunction04(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadInputRegisters, 0x41D9, 0x5C29))

	values, err := c.ReadInputValues("1004:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadInputValues: %v", err)
	}
	if len(values) != 1 || math.Abs(float64(values[0])-27.17) > 0.01 {
		t.Fatalf("ReadInputValues = %v, want 约 27.17", values)
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x04, 0x03, 0xEC, 0x00, 0x02}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestReadValuesMixedKinds(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, 100, 0xFFFF, 0x3FC0, 0x0000))

	values, err := c.ReadValues("0:uint16,1:int16,2:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	want := []float32{100, -1, 1.5}
	if len(values) != len(want) {
		t.Fatalf("ReadValues = %v, want %v", values, want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("ReadValues[%d] = %v, want %v", i, values[i], want[i])
		}
	}
	if mt.writeCount() != 1 {
		t.Fatalf("连续的三项应合并为一次请求，实际发了 %d 帧", mt.writeCount())
	}
}

func TestReadValuesSplitsAtQuantityLimit(t *testing.T) {
	// 126 个连续的单寄存器条目超过一次请求的上限（125），必须拆成两帧。
	const count = MaxReadRegisters + 1
	specs := make([]string, count)
	first := make([]uint16, MaxReadRegisters)
	for i := 0; i < count; i++ {
		specs[i] = fmt.Sprintf("%d:uint16", i)
		if i < MaxReadRegisters {
			first[i] = uint16(i)
		}
	}
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, first...),
		registerReply(0x01, FuncReadHoldingRegisters, uint16(count-1)))

	values, err := c.ReadValues(strings.Join(specs, ","), ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	if len(values) != count {
		t.Fatalf("ReadValues 返回 %d 个值, want %d", len(values), count)
	}
	for i, v := range values {
		if v != float32(i) {
			t.Fatalf("values[%d] = %v, want %d", i, v, i)
		}
	}
	if mt.writeCount() != 2 {
		t.Fatalf("发送了 %d 帧, want 2", mt.writeCount())
	}
}

func TestReadValuesPropagatesTransportError(t *testing.T) {
	c, _ := newTestClient(t, Config{UnitID: 0x01, Timeout: 20 * time.Millisecond})
	if _, err := c.ReadValues("1004:float32", ABCD); !errors.Is(err, ErrTimeout) {
		t.Fatalf("ReadValues = %v, want ErrTimeout", err)
	}
}

func TestReadValuesReportsException(t *testing.T) {
	reply := rtuReplyFunc(0x01, FuncReadHoldingRegisters|0x80, byte(ExceptionIllegalDataAddress))
	c, _ := newTestClient(t, Config{UnitID: 0x01}, reply)
	_, err := c.ReadValues("1004:float32", ABCD)
	var ex *ExceptionError
	if !errors.As(err, &ex) {
		t.Fatalf("ReadValues = %v, want *ExceptionError", err)
	}
	if !strings.Contains(err.Error(), "1004") {
		t.Fatalf("错误信息应指出出错的地址，实际为 %q", err.Error())
	}
}

func TestReadValuesByteOrders(t *testing.T) {
	// 同一组寄存器（0x3FC0 0x0000，按 ABCD 是 float32 的 1.5）在四种字节序下的解读结果。
	// 用位模式书写期望值，避免手写十进制近似值带来的误差。
	tests := []struct {
		order ByteOrder
		want  float32
	}{
		{ABCD, math.Float32frombits(0x3FC00000)},
		{CDAB, math.Float32frombits(0x00003FC0)},
		{BADC, math.Float32frombits(0xC03F0000)},
		{DCBA, math.Float32frombits(0x0000C03F)},
	}
	if tests[0].want != 1.5 {
		t.Fatalf("前提错误：ABCD 应为 1.5，实际 %v", tests[0].want)
	}
	for _, tt := range tests {
		t.Run(tt.order.String(), func(t *testing.T) {
			c, _ := newTestClient(t, Config{UnitID: 0x01},
				registerReply(0x01, FuncReadHoldingRegisters, 0x3FC0, 0x0000))
			values, err := c.ReadValues("0:float32", tt.order)
			if err != nil {
				t.Fatalf("ReadValues: %v", err)
			}
			if values[0] != tt.want {
				t.Fatalf("ReadValues(%s) = %v, want %v", tt.order, values[0], tt.want)
			}
		})
	}
}

// writeSingleReply 构造 0x06 的响应（回显地址与数值）。
func writeSingleReply(unitID byte, address, value uint16) []byte {
	return rtuReplyFunc(unitID, FuncWriteSingleRegister,
		byte(address>>8), byte(address), byte(value>>8), byte(value))
}

// writeMultipleReply 构造 0x10 的响应（回显地址与数量）。
func writeMultipleReply(unitID byte, address, quantity uint16) []byte {
	return rtuReplyFunc(unitID, FuncWriteMultipleRegisters,
		byte(address>>8), byte(address), byte(quantity>>8), byte(quantity))
}

func TestWriteValuesSingleRegisterUsesFunction06(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeSingleReply(0x01, 16, 1234))

	if err := c.WriteValues("16:1234:uint16", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("发送了 %d 帧, want 1", mt.writeCount())
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x06, 0x00, 0x10, 0x04, 0xD2}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestWriteValuesMergesAdjacentEntries(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeMultipleReply(0x01, 1004, 4))

	// 两个 float32 首尾相接，应合并成一次 0x10 写入 4 个寄存器。
	if err := c.WriteValues("1004:27.17:float32,1006:55.16:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("首尾相接的两项应合并为一次请求，实际发了 %d 帧", mt.writeCount())
	}
	frame := mt.frame(0)
	want := []byte{
		0x01, 0x10, 0x03, 0xEC, 0x00, 0x04, 0x08,
		0x41, 0xD9, 0x5C, 0x29, // 27.17
		0x42, 0x5C, 0xA3, 0xD7, // 55.16
	}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestWriteValuesNonAdjacentEntries(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		writeMultipleReply(0x01, 1004, 2),
		writeMultipleReply(0x01, 1010, 2))

	if err := c.WriteValues("1004:1.5:float32,1010:-2.25:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("不连续的两项应发两帧，实际发了 %d 帧", mt.writeCount())
	}
	first := mt.frame(0)
	if want := []byte{0x01, 0x10, 0x03, 0xEC, 0x00, 0x02, 0x04, 0x3F, 0xC0, 0x00, 0x00}; !bytes.Equal(first[:len(first)-2], want) {
		t.Fatalf("第 1 帧 = % X, want % X", first, want)
	}
	second := mt.frame(1)
	if want := []byte{0x01, 0x10, 0x03, 0xF2, 0x00, 0x02, 0x04, 0xC0, 0x10, 0x00, 0x00}; !bytes.Equal(second[:len(second)-2], want) {
		t.Fatalf("第 2 帧 = % X, want % X", second, want)
	}
}

func TestWriteValuesFollowsAddressOrder(t *testing.T) {
	// 清单里 1010 在前，但总线请求按地址升序发出。
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		writeMultipleReply(0x01, 1004, 2),
		writeMultipleReply(0x01, 1010, 2))

	if err := c.WriteValues("1010:-2.25:float32,1004:1.5:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("发送了 %d 帧, want 2", mt.writeCount())
	}
	first := mt.frame(0)
	if first[3] != 0xEC { // 1004
		t.Fatalf("第 1 帧地址 = %d, want 1004", uint16(first[3])<<8|uint16(first[4]))
	}
}

func TestWriteValuesMixedKinds(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeMultipleReply(0x01, 0, 4))

	if err := c.WriteValues("0:100:uint16,1:-1:int16,2:1.5:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("连续的三项应合并为一次请求，实际发了 %d 帧", mt.writeCount())
	}
	frame := mt.frame(0)
	want := []byte{
		0x01, 0x10, 0x00, 0x00, 0x00, 0x04, 0x08,
		0x00, 0x64, // 100
		0xFF, 0xFF, // -1
		0x3F, 0xC0, 0x00, 0x00, // 1.5
	}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestWriteValuesByteOrder(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeMultipleReply(0x01, 0, 2))

	if err := c.WriteValues("0:1.5:float32", CDAB); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x10, 0x00, 0x00, 0x00, 0x02, 0x04, 0x00, 0x00, 0x3F, 0xC0}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestWriteValuesIntegerForms(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want []uint16
	}{
		{"十进制 uint16", "0:1234:uint16", []uint16{1234}},
		{"十六进制 uint16", "0:0x04D2:uint16", []uint16{1234}},
		{"负 int16", "0:-2:int16", []uint16{0xFFFE}},
		{"uint32", "0:0x00010002:uint32", []uint16{0x0001, 0x0002}},
		{"int32 负数", "0:-2:int32", []uint16{0xFFFF, 0xFFFE}},
		{"float32 整数写法", "0:2:float32", []uint16{0x4000, 0x0000}},
		{"float64", "0:1.5:float64", []uint16{0x3FF8, 0x0000, 0x0000, 0x0000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := parseWriteSpec(tt.spec, ABCD)
			if err != nil {
				t.Fatalf("parseWriteSpec(%q): %v", tt.spec, err)
			}
			got := entries[0].registers
			if len(got) != len(tt.want) {
				t.Fatalf("%q 编码出 %d 个寄存器, want %d", tt.spec, len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("%q 编码 = % X, want % X", tt.spec, got, tt.want)
				}
			}
		})
	}
}

func TestWriteValuesErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"空清单", ""},
		{"空项", ",1004:1.5:float32"},
		{"缺段", "1004:1.5"},
		{"段太多", "1004:1.5:float32:extra"},
		{"地址非法", "abc:1.5:float32"},
		{"数值非法", "1004:abc:float32"},
		{"数值超出 uint16", "1004:70000:uint16"},
		{"负数给 uint16", "1004:-1:uint16"},
		{"小数给 uint16", "1004:1.5:uint16"},
		{"类型不支持", "1004:1.5:float16"},
		{"末尾空项", "1004:1.5:float32,"},
		{"只有两段", "1004:float32"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseWriteSpec(tt.spec, ABCD); !errors.Is(err, ErrSpec) {
				t.Fatalf("parseWriteSpec(%q) = %v, want ErrSpec", tt.spec, err)
			}
		})
	}
}

func TestWriteValuesBroadcast(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0})
	if err := c.WriteValues("1004:27.17:float32,1006:55.16:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("发送了 %d 帧, want 1", mt.writeCount())
	}
	if frame := mt.frame(0); frame[0] != 0 {
		t.Fatalf("广播帧的从站地址 = %d, want 0", frame[0])
	}
}

func TestWriteValuesEchoMismatch(t *testing.T) {
	// 从站回显了别的数值，应报 ErrFrame。
	c, _ := newTestClient(t, Config{UnitID: 0x01}, writeSingleReply(0x01, 16, 9999))
	if err := c.WriteValues("16:1234:uint16", ABCD); !errors.Is(err, ErrFrame) {
		t.Fatalf("WriteValues = %v, want ErrFrame", err)
	}
}

func TestWriteThenReadBack(t *testing.T) {
	// 写入 27.17 之后再读回来，应得到同一个数值（读写共用同一套字节序规则）。
	c, _ := newTestClient(t, Config{UnitID: 0x01},
		writeMultipleReply(0x01, 1004, 2),
		registerReply(0x01, FuncReadHoldingRegisters, 0x41D9, 0x5C29))

	if err := c.WriteValues("1004:27.17:float32", ABCD); err != nil {
		t.Fatalf("WriteValues: %v", err)
	}
	values, err := c.ReadValues("1004:float32", ABCD)
	if err != nil {
		t.Fatalf("ReadValues: %v", err)
	}
	if math.Abs(float64(values[0])-27.17) > 1e-4 {
		t.Fatalf("读回 %v, want 约 27.17", values[0])
	}
}
