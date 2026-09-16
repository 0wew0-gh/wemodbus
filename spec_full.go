package wemodbus

import (
	"strconv"
	"strings"
)

// specHead 是完整清单的第一部分：功能码、字节序与可选的总长度。
type specHead struct {
	function byte
	order    ByteOrder
	total    uint16
	hasTotal bool
}

// ReadBySpec 解析一条带头的完整读取清单并执行读取。
//
// 清单形如：
//
//	"04:ABCD:4;1004:float32,1006:float32"
//
// 以分号分成两部分：
//
//   - 第一部分 "功能码:字节序[:总长度]"：功能码 03 读保持寄存器、04 读输入寄存器；
//     字节序取 ABCD / CDAB / BADC / DCBA（不区分大小写）；总长度是从首个条目地址
//     开始连续读取的寄存器个数，可以省略。
//   - 第二部分是逗号分隔的条目 "地址:类型"，规则与 ReadValues 相同。
//
// 给出总长度时整段只发一次请求，并要求条目首尾相接、恰好铺满这一段寄存器：
// "04:ABCD:4;1004:float32,1006:float32" 读 1004~1007 共 4 个寄存器；写成
// "04:ABCD:4;1004:float32,1007:float32" 则因为第二项应从 1006 开始而返回
// ErrSpec，错误文本会给出对不上的项与应有的地址。省略总长度时各条目分别读取。
//
// 返回值与 ReadValues 一致：按清单顺序给出 []float32。
func (c *Client) ReadBySpec(spec string) ([]float32, error) {
	head, body, err := splitSpecHead(spec)
	if err != nil {
		return nil, err
	}
	if head.function != FuncReadHoldingRegisters && head.function != FuncReadInputRegisters {
		return nil, fail(ErrSpec, "read spec function code must be 03 (holding) or 04 (input), got 0x%02X", head.function)
	}

	entries, err := parseSpec(body)
	if err != nil {
		return nil, err
	}

	if !head.hasTotal {
		return c.readSpecEntries(head.function, entries, head.order, false)
	}
	if int(head.total) > MaxReadRegisters {
		return nil, fail(ErrSpec, "total length %d exceeds the read limit %d", head.total, MaxReadRegisters)
	}
	if err := checkCoverage(readCoverage(entries), head.total); err != nil {
		return nil, err
	}
	return c.readSpecEntries(head.function, entries, head.order, true)
}

// WriteBySpec 解析一条带头的完整写入清单并执行写入。
//
// 清单形如：
//
//	"10:ABCD:4;1004:27.17:float32,1006:55.16:float32"
//
// 第一部分与 ReadBySpec 相同，功能码 06 表示写单个寄存器、10 表示写多个寄存器；
// 第二部分是逗号分隔的条目 "地址:数值:类型"，规则与 WriteValues 相同。
//
// 给出总长度时整段只发一次写请求，同样要求条目首尾相接、恰好铺满这一段寄存器；
// 省略总长度时各条目分别写入。功能码 06 只允许覆盖 1 个寄存器。
func (c *Client) WriteBySpec(spec string) error {
	head, body, err := splitSpecHead(spec)
	if err != nil {
		return err
	}
	if head.function != FuncWriteSingleRegister && head.function != FuncWriteMultipleRegisters {
		return fail(ErrSpec, "write spec function code must be 06 (single) or 10 (multiple), got 0x%02X", head.function)
	}

	entries, err := parseWriteSpec(body, head.order)
	if err != nil {
		return err
	}
	if head.function == FuncWriteSingleRegister {
		for _, entry := range entries {
			if len(entry.registers) != 1 {
				return fail(ErrSpec, "function 06 can only write 1 register, item %d takes %d", entry.index+1, len(entry.registers))
			}
		}
	}

	if !head.hasTotal {
		return c.writeSpecEntries(head.function, entries, false)
	}
	if int(head.total) > MaxWriteRegisters {
		return fail(ErrSpec, "total length %d exceeds the write limit %d", head.total, MaxWriteRegisters)
	}
	if err := checkCoverage(writeCoverage(entries), head.total); err != nil {
		return err
	}
	return c.writeSpecEntries(head.function, entries, true)
}

// splitSpecHead 把完整清单拆成头部与条目两部分，并解析头部。
func splitSpecHead(spec string) (specHead, string, error) {
	head, body, found := strings.Cut(spec, ";")
	if !found {
		return specHead{}, "", fail(ErrSpec, "missing semicolon, a full spec looks like 04:ABCD:4;1004:float32")
	}
	head, body = strings.TrimSpace(head), strings.TrimSpace(body)
	if head == "" {
		return specHead{}, "", fail(ErrSpec, "missing function:order[:total] before semicolon")
	}
	if body == "" {
		return specHead{}, "", fail(ErrSpec, "missing items after semicolon")
	}
	if strings.Contains(body, ";") {
		return specHead{}, "", fail(ErrSpec, "a full spec can contain only one semicolon")
	}

	parts := strings.Split(head, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return specHead{}, "", fail(ErrSpec, "head %q is not function:order[:total]", head)
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	function, err := parseFunctionCode(parts[0])
	if err != nil {
		return specHead{}, "", err
	}
	order, err := parseByteOrderName(parts[1])
	if err != nil {
		return specHead{}, "", err
	}

	out := specHead{function: function, order: order}
	if len(parts) == 3 {
		total, err := strconv.ParseUint(parts[2], 10, 16)
		if err != nil || total == 0 {
			return specHead{}, "", fail(ErrSpec, "total length %q is not a positive integer", parts[2])
		}
		out.total, out.hasTotal = uint16(total), true
	}
	return out, body, nil
}

// parseFunctionCode 解析完整清单头部的功能码。这里按十六进制读，与协议手册的
// 写法一致：03、04、06、10（也接受 0x 前缀）。读用 03/04，写用 06/10。
func parseFunctionCode(text string) (byte, error) {
	clean := strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	value, err := strconv.ParseUint(clean, 16, 8)
	if err != nil {
		return 0, fail(ErrSpec, "function code %q is not hexadecimal", text)
	}
	switch byte(value) {
	case FuncReadHoldingRegisters, FuncReadInputRegisters,
		FuncWriteSingleRegister, FuncWriteMultipleRegisters:
		return byte(value), nil
	default:
		return 0, fail(ErrSpec, "function code 0x%02X is not supported (read: 03/04, write: 06/10)", value)
	}
}

// parseByteOrderName 解析完整清单头部的字节序名称。
func parseByteOrderName(text string) (ByteOrder, error) {
	switch strings.ToUpper(text) {
	case "ABCD":
		return ABCD, nil
	case "CDAB":
		return CDAB, nil
	case "BADC":
		return BADC, nil
	case "DCBA":
		return DCBA, nil
	default:
		return 0, fail(ErrSpec, "byte order %q is not supported (available: ABCD / CDAB / BADC / DCBA)", text)
	}
}

// coverageItem 是校验条目覆盖范围时用到的最小信息。
type coverageItem struct {
	address uint16
	length  int
	index   int
}

func readCoverage(entries []specEntry) []coverageItem {
	items := make([]coverageItem, len(entries))
	for i, entry := range entries {
		items[i] = coverageItem{address: entry.address, length: int(entry.length), index: entry.index}
	}
	return items
}

func writeCoverage(entries []writeEntry) []coverageItem {
	items := make([]coverageItem, len(entries))
	for i, entry := range entries {
		items[i] = coverageItem{address: entry.address, length: len(entry.registers), index: entry.index}
	}
	return items
}

// checkCoverage 校验条目首尾相接，且恰好铺满从首个地址开始、长度为 total 的一段
// 寄存器。地址对不上时会指出是第几项、应为多少。
func checkCoverage(items []coverageItem, total uint16) error {
	if len(items) == 0 {
		return fail(ErrSpec, "spec has no items")
	}
	start, next, sum := items[0].address, int(items[0].address), 0
	for _, item := range items {
		if int(item.address) != next {
			return fail(ErrSpec, "item %d address %d does not match the declared total: the spec starts at %d and covers %d registers, so this item should start at %d", item.index+1, item.address, start, total, next)
		}
		next += item.length
		sum += item.length
	}
	if sum != int(total) {
		return fail(ErrSpec, "items cover %d registers (%d~%d), but the declared total is %d", sum, start, next-1, total)
	}
	return nil
}
