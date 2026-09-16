package wemodbus

// 本文件是价值语义的薄封装：把「读 2 个/4 个寄存器」与字节序解码组合起来，
// 内部固定使用功能码 0x03（保持寄存器）。需要读写输入寄存器（0x04）时
// 请直接调用 ReadInputRegisters 配合 value.go 中的编解码函数。

// ReadUint32 读取从 address 开始的 2 个保持寄存器并按 o 解码为无符号整数。
func (c *Client) ReadUint32(address uint16, o ByteOrder) (uint32, error) {
	registers, err := c.ReadHoldingRegisters(address, 2)
	if err != nil {
		return 0, err
	}
	return RegistersToUint32(registers, o), nil
}

// ReadInt32 读取从 address 开始的 2 个保持寄存器并按 o 解码为有符号整数。
func (c *Client) ReadInt32(address uint16, o ByteOrder) (int32, error) {
	registers, err := c.ReadHoldingRegisters(address, 2)
	if err != nil {
		return 0, err
	}
	return RegistersToInt32(registers, o), nil
}

// ReadFloat32 读取从 address 开始的 2 个保持寄存器并按 o 解码为单精度浮点数。
func (c *Client) ReadFloat32(address uint16, o ByteOrder) (float32, error) {
	registers, err := c.ReadHoldingRegisters(address, 2)
	if err != nil {
		return 0, err
	}
	return RegistersToFloat32(registers, o), nil
}

// ReadUint64 读取从 address 开始的 4 个保持寄存器并按 o 解码为无符号整数。
func (c *Client) ReadUint64(address uint16, o ByteOrder) (uint64, error) {
	registers, err := c.ReadHoldingRegisters(address, 4)
	if err != nil {
		return 0, err
	}
	return RegistersToUint64(registers, o), nil
}

// ReadInt64 读取从 address 开始的 4 个保持寄存器并按 o 解码为有符号整数。
func (c *Client) ReadInt64(address uint16, o ByteOrder) (int64, error) {
	registers, err := c.ReadHoldingRegisters(address, 4)
	if err != nil {
		return 0, err
	}
	return RegistersToInt64(registers, o), nil
}

// ReadFloat64 读取从 address 开始的 4 个保持寄存器并按 o 解码为双精度浮点数。
func (c *Client) ReadFloat64(address uint16, o ByteOrder) (float64, error) {
	registers, err := c.ReadHoldingRegisters(address, 4)
	if err != nil {
		return 0, err
	}
	return RegistersToFloat64(registers, o), nil
}

// WriteUint32 按 o 把 v 编码为 2 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteUint32(address uint16, v uint32, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Uint32ToRegisters(v, o))
}

// WriteInt32 按 o 把 v 的补码编码为 2 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteInt32(address uint16, v int32, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Int32ToRegisters(v, o))
}

// WriteFloat32 按 o 把 v 编码为 2 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteFloat32(address uint16, v float32, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Float32ToRegisters(v, o))
}

// WriteUint64 按 o 把 v 编码为 4 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteUint64(address uint16, v uint64, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Uint64ToRegisters(v, o))
}

// WriteInt64 按 o 把 v 的补码编码为 4 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteInt64(address uint16, v int64, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Int64ToRegisters(v, o))
}

// WriteFloat64 按 o 把 v 编码为 4 个保持寄存器并写入 address 开始的地址。
func (c *Client) WriteFloat64(address uint16, v float64, o ByteOrder) error {
	return c.WriteMultipleRegisters(address, Float64ToRegisters(v, o))
}
