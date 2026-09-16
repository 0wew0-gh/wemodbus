package wemodbus

import (
	"sort"
	"strconv"
	"strings"
)

// specKinds 是寄存器清单支持的类型，以及每种类型占用的寄存器个数。
// 寄存器是 16 位的，所以 32 位类型占 2 个、64 位类型占 4 个。
var specKinds = map[string]uint16{
	"uint16":  1,
	"int16":   1,
	"uint32":  2,
	"int32":   2,
	"float32": 2,
	"uint64":  4,
	"int64":   4,
	"float64": 4,
}

// specEntry 是读取清单中的一项。
type specEntry struct {
	address uint16 // 寄存器起始地址
	length  uint16 // 占用的寄存器个数
	kind    string // 解码类型
	index   int    // 该项在返回值中的位置
}

// writeEntry 是写入清单中的一项，registers 已经按字节序编码完成。
type writeEntry struct {
	address   uint16
	registers []uint16
	index     int
}

// ReadValues 按清单读取保持寄存器（功能码 0x03），并按 order 解码为 []float32。
//
// spec 是用逗号分隔的一组 "地址:类型" 条目，例如：
//
//	"1004:float32,1006:float32"
//
// 地址是十进制（允许 0x 前缀），类型决定读几个寄存器以及如何解释它们：
//
//	uint16 / int16             占用 1 个寄存器
//	uint32 / int32 / float32   占用 2 个寄存器
//	uint64 / int64 / float64   占用 4 个寄存器
//
// 返回值的长度与条目数相同、顺序与 spec 一致；所有类型都统一换算成 float32
// （64 位整数或双精度浮点会有精度损失）。
//
// 地址首尾相接的条目会被合并成一次总线请求（单次不超过 MaxReadRegisters），
// 因此 "1004:float32,1006:float32" 只发一帧就能读回两个数值。
// 清单无法解析、类型不支持时返回 ErrSpec。
func (c *Client) ReadValues(spec string, order ByteOrder) ([]float32, error) {
	return c.readSpec(spec, FuncReadHoldingRegisters, order)
}

// ReadInputValues 与 ReadValues 相同，但读取的是输入寄存器（功能码 0x04）。
// 输入寄存器只读，因此没有对应的写方法。
func (c *Client) ReadInputValues(spec string, order ByteOrder) ([]float32, error) {
	return c.readSpec(spec, FuncReadInputRegisters, order)
}

// WriteValues 按清单写入保持寄存器，第二段是数值而不是寄存器个数：
//
//	"1004:27.17:float32,1006:55.16:float32"
//
// 每一项形如 "地址:数值:类型"：数值按类型编码成 1/2/4 个寄存器，从该地址开始
// 连续写入。整数类型（uint16/int16/uint32/int32/uint64/int64）支持 0x 前缀的
// 十六进制写法，浮点类型用十进制或科学计数法。
//
// 地址首尾相接的条目会合并成一次写请求（单次不超过 MaxWriteRegisters）：
// 只覆盖一个寄存器时用 0x06，多个寄存器时用 0x10。数值无法按类型解析、
// 类型不支持时返回 ErrSpec。
func (c *Client) WriteValues(spec string, order ByteOrder) error {
	entries, err := parseWriteSpec(spec, order)
	if err != nil {
		return err
	}
	return c.writeSpecEntries(0, entries, true)
}

// writeSpecEntries 写入已解析的条目：merge 为 true 时把首尾相接的条目合并成一次
// 请求，否则逐条写入。function 为 0 时按寄存器个数自动选择 0x06 或 0x10。
func (c *Client) writeSpecEntries(function byte, entries []writeEntry, merge bool) error {
	if !merge {
		for _, entry := range entries {
			if err := c.writeBlock(function, entry.address, entry.registers); err != nil {
				return errorWrap(err, "write %d registers from address %d failed", len(entry.registers), entry.address)
			}
		}
		return nil
	}

	// 按地址排序后把首尾相接的条目合并成一段，减少总线往返。
	sorted := make([]writeEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].address < sorted[j].address })

	for start := 0; start < len(sorted); {
		end, total := start+1, len(sorted[start].registers)
		for end < len(sorted) &&
			int(sorted[end].address) == int(sorted[end-1].address)+len(sorted[end-1].registers) &&
			total+len(sorted[end].registers) <= MaxWriteRegisters {
			total += len(sorted[end].registers)
			end++
		}

		values := make([]uint16, 0, total)
		for _, entry := range sorted[start:end] {
			values = append(values, entry.registers...)
		}
		if err := c.writeBlock(function, sorted[start].address, values); err != nil {
			return errorWrap(err, "write %d registers from address %d failed", total, sorted[start].address)
		}
		start = end
	}
	return nil
}

// readSpec 是 ReadValues / ReadInputValues 的共同实现。
func (c *Client) readSpec(spec string, function byte, order ByteOrder) ([]float32, error) {
	entries, err := parseSpec(spec)
	if err != nil {
		return nil, err
	}
	return c.readSpecEntries(function, entries, order, true)
}

// readSpecEntries 读取已解析的条目：merge 为 true 时把首尾相接的条目合并成一次
// 请求，否则逐条读取。
func (c *Client) readSpecEntries(function byte, entries []specEntry, order ByteOrder, merge bool) ([]float32, error) {
	values := make([]float32, len(entries))

	if !merge {
		for _, entry := range entries {
			registers, err := c.readRegisterBlock(function, entry.address, entry.length)
			if err != nil {
				return nil, errorWrap(err, "read %d registers from address %d failed", entry.length, entry.address)
			}
			value, err := decodeSpecValue(registers, entry.kind, order)
			if err != nil {
				return nil, err
			}
			values[entry.index] = value
		}
		return values, nil
	}

	// 按地址排序后把首尾相接的条目合并成一段，减少总线往返。
	sorted := make([]specEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].address < sorted[j].address })

	for start := 0; start < len(sorted); {
		end, total := start+1, int(sorted[start].length)
		for end < len(sorted) &&
			int(sorted[end].address) == int(sorted[end-1].address)+int(sorted[end-1].length) &&
			total+int(sorted[end].length) <= MaxReadRegisters {
			total += int(sorted[end].length)
			end++
		}

		registers, err := c.readRegisterBlock(function, sorted[start].address, uint16(total))
		if err != nil {
			return nil, errorWrap(err, "read %d registers from address %d failed", total, sorted[start].address)
		}
		for _, entry := range sorted[start:end] {
			offset := int(entry.address) - int(sorted[start].address)
			value, err := decodeSpecValue(registers[offset:offset+int(entry.length)], entry.kind, order)
			if err != nil {
				return nil, err
			}
			values[entry.index] = value
		}
		start = end
	}
	return values, nil
}

// readRegisterBlock 按功能码读取一段连续的寄存器。
func (c *Client) readRegisterBlock(function byte, address, quantity uint16) ([]uint16, error) {
	if function == FuncReadInputRegisters {
		return c.ReadInputRegisters(address, quantity)
	}
	return c.ReadHoldingRegisters(address, quantity)
}

// writeBlock 写入一段连续的保持寄存器。function 为 0 时按寄存器个数自动选择：
// 只写 1 个用 0x06，多个用 0x10。
func (c *Client) writeBlock(function byte, address uint16, values []uint16) error {
	if function == FuncWriteSingleRegister && len(values) != 1 {
		return fail(ErrSpec, "function 06 can only write 1 register, got %d", len(values))
	}
	if function == FuncWriteMultipleRegisters || len(values) > 1 {
		return c.WriteMultipleRegisters(address, values)
	}
	return c.WriteSingleRegister(address, values[0])
}

// specItem 是拆分并初步校验后的一项清单。
type specItem struct {
	index   int    // 第几项，从 0 开始
	address uint16 // 寄存器地址
	second  string // 写入清单里的数值；读取清单没有这一段
	kind    string // 已转小写并校验过的类型
}

// splitSpecItems 按逗号拆分清单，逐项校验地址与类型。
//
// 读取清单每项是 "地址:类型"（secondName 传空串）；写入清单每项是
// "地址:数值:类型"（secondName 传 "数值"）。
func splitSpecItems(spec, secondName string) ([]specItem, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fail(ErrSpec, "spec is empty")
	}

	wantParts, layout := 2, "地址:类型"
	if secondName != "" {
		wantParts, layout = 3, "地址:"+secondName+":类型"
	}

	raw := strings.Split(spec, ",")
	items := make([]specItem, 0, len(raw))
	for i, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil, fail(ErrSpec, "item %d is empty", i+1)
		}

		parts := strings.Split(line, ":")
		if len(parts) != wantParts {
			return nil, fail(ErrSpec, "item %d %q is not %s", i+1, line, layout)
		}
		for j := range parts {
			parts[j] = strings.TrimSpace(parts[j])
		}

		address, err := strconv.ParseUint(parts[0], 0, 16)
		if err != nil {
			return nil, fail(ErrSpec, "item %d: address %q is not an integer in 0~65535", i+1, parts[0])
		}
		kind := strings.ToLower(parts[wantParts-1])
		if _, ok := specKinds[kind]; !ok {
			return nil, fail(ErrSpec, "item %d: type %q is not supported (available: %s)", i+1, parts[wantParts-1], strings.Join(specKindNames(), " / "))
		}

		item := specItem{index: i, address: uint16(address), kind: kind}
		if wantParts == 3 {
			item.second = parts[1]
		}
		items = append(items, item)
	}
	return items, nil
}

// parseSpec 解析读取清单：每项是 "地址:类型"，占用的寄存器个数由类型决定。
func parseSpec(spec string) ([]specEntry, error) {
	items, err := splitSpecItems(spec, "")
	if err != nil {
		return nil, err
	}

	entries := make([]specEntry, 0, len(items))
	for _, item := range items {
		entries = append(entries, specEntry{
			address: item.address,
			length:  specKinds[item.kind],
			kind:    item.kind,
			index:   item.index,
		})
	}
	return entries, nil
}

// parseWriteSpec 解析写入清单（第二段是数值），并立即按字节序编码为寄存器。
func parseWriteSpec(spec string, order ByteOrder) ([]writeEntry, error) {
	items, err := splitSpecItems(spec, "数值")
	if err != nil {
		return nil, err
	}

	entries := make([]writeEntry, 0, len(items))
	for _, item := range items {
		registers, err := encodeSpecValue(item.second, item.kind, order)
		if err != nil {
			return nil, fail(ErrSpec, "item %d: %s value %q cannot be parsed", item.index+1, item.kind, item.second)
		}
		entries = append(entries, writeEntry{
			address:   item.address,
			registers: registers,
			index:     item.index,
		})
	}
	return entries, nil
}

// specKindNames 返回支持的类型名，按寄存器个数与名称排序，用于错误提示。
func specKindNames() []string {
	names := make([]string, 0, len(specKinds))
	for name := range specKinds {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if specKinds[names[i]] != specKinds[names[j]] {
			return specKinds[names[i]] < specKinds[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// decodeSpecValue 把一项所需的寄存器解码为 float32。
func decodeSpecValue(registers []uint16, kind string, order ByteOrder) (float32, error) {
	switch kind {
	case "uint16":
		return float32(registers[0]), nil
	case "int16":
		return float32(int16(registers[0])), nil
	case "uint32":
		return float32(RegistersToUint32(registers, order)), nil
	case "int32":
		return float32(RegistersToInt32(registers, order)), nil
	case "float32":
		return RegistersToFloat32(registers, order), nil
	case "uint64":
		return float32(RegistersToUint64(registers, order)), nil
	case "int64":
		return float32(RegistersToInt64(registers, order)), nil
	case "float64":
		return float32(RegistersToFloat64(registers, order)), nil
	default:
		return 0, fail(ErrSpec, "type %q is not supported", kind)
	}
}

// encodeSpecValue 把文本数值按类型编码为寄存器。
func encodeSpecValue(text, kind string, order ByteOrder) ([]uint16, error) {
	switch kind {
	case "uint16":
		v, err := strconv.ParseUint(text, 0, 16)
		if err != nil {
			return nil, err
		}
		return []uint16{uint16(v)}, nil
	case "int16":
		v, err := strconv.ParseInt(text, 0, 16)
		if err != nil {
			return nil, err
		}
		return []uint16{uint16(int16(v))}, nil
	case "uint32":
		v, err := strconv.ParseUint(text, 0, 32)
		if err != nil {
			return nil, err
		}
		return Uint32ToRegisters(uint32(v), order), nil
	case "int32":
		v, err := strconv.ParseInt(text, 0, 32)
		if err != nil {
			return nil, err
		}
		return Int32ToRegisters(int32(v), order), nil
	case "float32":
		v, err := strconv.ParseFloat(text, 32)
		if err != nil {
			return nil, err
		}
		return Float32ToRegisters(float32(v), order), nil
	case "uint64":
		v, err := strconv.ParseUint(text, 0, 64)
		if err != nil {
			return nil, err
		}
		return Uint64ToRegisters(v, order), nil
	case "int64":
		v, err := strconv.ParseInt(text, 0, 64)
		if err != nil {
			return nil, err
		}
		return Int64ToRegisters(v, order), nil
	case "float64":
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, err
		}
		return Float64ToRegisters(v, order), nil
	default:
		return nil, fail(ErrSpec, "type %q is not supported", kind)
	}
}
