package wemodbus

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReadBySpecOneRequest(t *testing.T) {
	// 用户给的例子：从 1004 起连续读 4 个输入寄存器，切成两个 float32。
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadInputRegisters, 0x3FC0, 0x0000, 0xC010, 0x0000))

	values, err := c.ReadBySpec("04:ABCD:4;1004:float32,1006:float32")
	if err != nil {
		t.Fatalf("ReadBySpec: %v", err)
	}
	if len(values) != 2 || values[0] != 1.5 || values[1] != -2.25 {
		t.Fatalf("ReadBySpec = %v, want [1.5 -2.25]", values)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("给了总长度应只发一帧，实际发了 %d 帧", mt.writeCount())
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x04, 0x03, 0xEC, 0x00, 0x04}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestReadBySpecFunction03(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadHoldingRegisters, 0x3FC0, 0x0000))

	values, err := c.ReadBySpec("03:ABCD:2;1004:float32")
	if err != nil {
		t.Fatalf("ReadBySpec: %v", err)
	}
	if values[0] != 1.5 {
		t.Fatalf("ReadBySpec = %v, want 1.5", values[0])
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x03, 0x03, 0xEC, 0x00, 0x02}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestReadBySpecWithoutTotalReadsSeparately(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		registerReply(0x01, FuncReadInputRegisters, 0x3FC0, 0x0000),
		registerReply(0x01, FuncReadInputRegisters, 0xC010, 0x0000))

	values, err := c.ReadBySpec("04:ABCD;1004:float32,1010:float32")
	if err != nil {
		t.Fatalf("ReadBySpec: %v", err)
	}
	if len(values) != 2 || values[0] != 1.5 || values[1] != -2.25 {
		t.Fatalf("ReadBySpec = %v, want [1.5 -2.25]", values)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("省略总长度应逐条读取，实际发了 %d 帧", mt.writeCount())
	}
}

func TestReadBySpecAddressMismatch(t *testing.T) {
	// 总长度说 4 个寄存器（1004~1007），第二项却写 1007，应从 1006 开始。
	c, mt := newTestClient(t, Config{UnitID: 0x01})

	_, err := c.ReadBySpec("04:ABCD:4;1004:float32,1007:float32")
	if !errors.Is(err, ErrSpec) {
		t.Fatalf("ReadBySpec = %v, want ErrSpec", err)
	}
	for _, want := range []string{"1007", "1006", "1004", "4"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息 %q 里应包含 %q", err.Error(), want)
		}
	}
	if mt.writeCount() != 0 {
		t.Fatalf("清单校验失败时不应发起请求，实际发了 %d 帧", mt.writeCount())
	}
}

func TestReadBySpecCoverageMismatch(t *testing.T) {
	c, _ := newTestClient(t, Config{UnitID: 0x01})

	// 总长度 6，但条目只覆盖 4 个寄存器。
	_, err := c.ReadBySpec("04:ABCD:6;1004:float32,1006:float32")
	if !errors.Is(err, ErrSpec) {
		t.Fatalf("ReadBySpec = %v, want ErrSpec", err)
	}
	for _, want := range []string{"4", "6"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息 %q 里应同时给出覆盖数与声明数（缺 %q）", err.Error(), want)
		}
	}

	// 总长度 2，条目覆盖了 4 个寄存器。
	if _, err := c.ReadBySpec("04:ABCD:2;1004:float32,1006:float32"); !errors.Is(err, ErrSpec) {
		t.Fatalf("ReadBySpec = %v, want ErrSpec", err)
	}
}

func TestReadBySpecByteOrderAndOverLimit(t *testing.T) {
	c, _ := newTestClient(t, Config{UnitID: 0x01}, registerReply(0x01, FuncReadInputRegisters, 0x3FC0, 0x0000))
	values, err := c.ReadBySpec("04:cdab:2;1004:float32")
	if err != nil {
		t.Fatalf("ReadBySpec: %v", err)
	}
	if values[0] != float32(2.2869e-41) && values[0] == 1.5 {
		t.Fatalf("CDAB 应改变解读结果，实际 %v", values[0])
	}

	if _, err := c.ReadBySpec("04:ABCD:126;0:uint16"); !errors.Is(err, ErrSpec) {
		t.Fatalf("总长度超上限 = %v, want ErrSpec", err)
	}
}

func TestReadBySpecHeadErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"缺分号", "04:ABCD:4"},
		{"分号前为空", ";1004:float32"},
		{"分号后为空", "04:ABCD:4;"},
		{"两个分号", "04:ABCD:4;1004:float32;1006:float32"},
		{"头部段太多", "04:ABCD:4:extra;1004:float32"},
		{"头部段太少", "04;1004:float32"},
		{"功能码不支持", "05:ABCD:4;1004:float32"},
		{"功能码非数字", "zz:ABCD:4;1004:float32"},
		{"功能码是写", "06:ABCD:1;1004:float32"},
		{"功能码是写多个", "10:ABCD:2;1004:float32"},
		{"字节序不支持", "04:XYZ:4;1004:float32"},
		{"总长度非数字", "04:ABCD:abc;1004:float32"},
		{"总长度为 0", "04:ABCD:0;1004:float32"},
		{"条目为空", "04:ABCD:4;1004:float32,,1006:float32"},
		{"条目类型不支持", "04:ABCD:2;1004:float16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mt := newTestClient(t, Config{UnitID: 0x01})
			if _, err := c.ReadBySpec(tt.spec); !errors.Is(err, ErrSpec) {
				t.Fatalf("ReadBySpec(%q) = %v, want ErrSpec", tt.spec, err)
			}
			if mt.writeCount() != 0 {
				t.Fatalf("清单非法时不应发起请求，实际发了 %d 帧", mt.writeCount())
			}
		})
	}
}

func TestWriteBySpecOneRequest(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeMultipleReply(0x01, 1004, 4))

	if err := c.WriteBySpec("10:ABCD:4;1004:27.17:float32,1006:55.16:float32"); err != nil {
		t.Fatalf("WriteBySpec: %v", err)
	}
	if mt.writeCount() != 1 {
		t.Fatalf("给了总长度应只发一帧，实际发了 %d 帧", mt.writeCount())
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

func TestWriteBySpecSingleRegister(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01}, writeSingleReply(0x01, 16, 1234))

	if err := c.WriteBySpec("06:ABCD:1;16:1234:uint16"); err != nil {
		t.Fatalf("WriteBySpec: %v", err)
	}
	frame := mt.frame(0)
	want := []byte{0x01, 0x06, 0x00, 0x10, 0x04, 0xD2}
	if !bytes.Equal(frame[:len(frame)-2], want) {
		t.Fatalf("请求 = % X, want % X + CRC", frame, want)
	}
}

func TestWriteBySpecWithoutTotalWritesSeparately(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01},
		writeMultipleReply(0x01, 1004, 2),
		writeMultipleReply(0x01, 1010, 2))

	if err := c.WriteBySpec("10:ABCD;1004:1.5:float32,1010:-2.25:float32"); err != nil {
		t.Fatalf("WriteBySpec: %v", err)
	}
	if mt.writeCount() != 2 {
		t.Fatalf("省略总长度应逐条写入，实际发了 %d 帧", mt.writeCount())
	}
}

func TestWriteBySpecAddressMismatch(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01})

	err := c.WriteBySpec("10:ABCD:4;1004:27.17:float32,1007:55.16:float32")
	if !errors.Is(err, ErrSpec) {
		t.Fatalf("WriteBySpec = %v, want ErrSpec", err)
	}
	if !strings.Contains(err.Error(), "1006") {
		t.Fatalf("错误信息 %q 应指出正确地址 1006", err.Error())
	}
	if mt.writeCount() != 0 {
		t.Fatalf("清单校验失败时不应发起请求，实际发了 %d 帧", mt.writeCount())
	}
}

func TestWriteBySpecFunction06RejectsMultiple(t *testing.T) {
	c, mt := newTestClient(t, Config{UnitID: 0x01})

	// 单个 float32 占 2 个寄存器，功能码 06 写不了。
	if err := c.WriteBySpec("06:ABCD:2;1004:1.5:float32"); !errors.Is(err, ErrSpec) {
		t.Fatalf("WriteBySpec = %v, want ErrSpec", err)
	}
	// 省略总长度时同样要拦住。
	if err := c.WriteBySpec("06:ABCD;1004:1.5:float32"); !errors.Is(err, ErrSpec) {
		t.Fatalf("WriteBySpec = %v, want ErrSpec", err)
	}
	if mt.writeCount() != 0 {
		t.Fatalf("清单非法时不应发起请求，实际发了 %d 帧", mt.writeCount())
	}
}

func TestWriteBySpecHeadErrors(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"缺分号", "10:ABCD:4"},
		{"功能码是读", "04:ABCD:2;1004:1.5:float32"},
		{"功能码不支持", "05:ABCD:1;1004:1:uint16"},
		{"字节序不支持", "10:XYZ:2;1004:1.5:float32"},
		{"数值非法", "10:ABCD:2;1004:abc:float32"},
		{"数值超出范围", "10:ABCD:1;16:70000:uint16"},
		{"总长度超上限", "10:ABCD:124;0:uint16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mt := newTestClient(t, Config{UnitID: 0x01})
			if err := c.WriteBySpec(tt.spec); !errors.Is(err, ErrSpec) {
				t.Fatalf("WriteBySpec(%q) = %v, want ErrSpec", tt.spec, err)
			}
			if mt.writeCount() != 0 {
				t.Fatalf("清单非法时不应发起请求，实际发了 %d 帧", mt.writeCount())
			}
		})
	}
}

func TestWriteThenReadBySpecRoundTrip(t *testing.T) {
	// 写入 27.17 后再读回，应得到同一个数值。
	c, _ := newTestClient(t, Config{UnitID: 0x01},
		writeMultipleReply(0x01, 1004, 2),
		registerReply(0x01, FuncReadInputRegisters, 0x41D9, 0x5C29))

	if err := c.WriteBySpec("10:ABCD:2;1004:27.17:float32"); err != nil {
		t.Fatalf("WriteBySpec: %v", err)
	}
	values, err := c.ReadBySpec("04:ABCD:2;1004:float32")
	if err != nil {
		t.Fatalf("ReadBySpec: %v", err)
	}
	if values[0] != 27.17 {
		t.Fatalf("读回 %v, want 27.17", values[0])
	}
}
