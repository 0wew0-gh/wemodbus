package wemodbus

import "sync"

// Handler 处理从站收到的请求。实现它就能把任意后端（数据库、PLC 内存映射、
// 共享内存等）接进从站；DataModel 提供了一个现成的内存实现。
//
// 返回值约定：返回 nil 表示成功；返回 *ExceptionError（用 Exception 构造）会让
// 从站回对应的异常响应；返回其它 error 一律按 0x04「从站设备故障」应答。
//
// 实现应当快速返回：从站必须在主站的响应超时之前写出应答，处理耗时过长会被
// 主站判为超时。
type Handler interface {
	ReadCoils(address, quantity uint16) ([]bool, error)
	ReadDiscreteInputs(address, quantity uint16) ([]bool, error)
	ReadHoldingRegisters(address, quantity uint16) ([]uint16, error)
	ReadInputRegisters(address, quantity uint16) ([]uint16, error)
	WriteSingleCoil(address uint16, on bool) error
	WriteSingleRegister(address, value uint16) error
	WriteMultipleCoils(address uint16, values []bool) error
	WriteMultipleRegisters(address uint16, values []uint16) error
}

// Exception 构造一个从站异常。Handler 返回它，从站就会把对应的异常码回给主站，
// 例如 Exception(ExceptionIllegalDataAddress)。
func Exception(code ExceptionCode) error {
	return &ExceptionError{Code: code}
}

// MaskWriteHandler 是 Handler 的可选扩展：实现了它就支持 0x16 掩码写寄存器，
// 否则从站对该功能码回 0x01「非法功能码」。DataModel 已经实现。
type MaskWriteHandler interface {
	MaskWriteRegister(address, andMask, orMask uint16) error
}

// ReadWriteHandler 是 Handler 的可选扩展：实现了它就支持 0x17 读写多个寄存器。
// DataModel 已经实现。
type ReadWriteHandler interface {
	ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress uint16, values []uint16) ([]uint16, error)
}

// DataModel 是内存数据区，本身就是一个 Handler，可以直接交给 NewServer。
//
// 四张表的长度在创建时固定，越界访问按 0x02「非法数据地址」应答。所有方法都
// 带锁，可以在从站处理请求的同时被应用侧并发读写。
type DataModel struct {
	mu               sync.RWMutex
	coils            []bool
	discreteInputs   []bool
	holdingRegisters []uint16
	inputRegisters   []uint16
}

// NewDataModel 创建数据区，四个参数依次是线圈、离散输入、保持寄存器、输入寄存器的数量。
func NewDataModel(coils, discreteInputs, holdingRegisters, inputRegisters int) *DataModel {
	return &DataModel{
		coils:            make([]bool, max(coils, 0)),
		discreteInputs:   make([]bool, max(discreteInputs, 0)),
		holdingRegisters: make([]uint16, max(holdingRegisters, 0)),
		inputRegisters:   make([]uint16, max(inputRegisters, 0)),
	}
}

// Sizes 返回四张表的长度。
func (m *DataModel) Sizes() (coils, discreteInputs, holdingRegisters, inputRegisters int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.coils), len(m.discreteInputs), len(m.holdingRegisters), len(m.inputRegisters)
}

// ReadCoils 读取一段线圈。
func (m *DataModel) ReadCoils(address, quantity uint16) ([]bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return readBits(m.coils, address, quantity)
}

// ReadDiscreteInputs 读取一段离散输入。
func (m *DataModel) ReadDiscreteInputs(address, quantity uint16) ([]bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return readBits(m.discreteInputs, address, quantity)
}

// ReadHoldingRegisters 读取一段保持寄存器。
func (m *DataModel) ReadHoldingRegisters(address, quantity uint16) ([]uint16, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return readRegisters(m.holdingRegisters, address, quantity)
}

// ReadInputRegisters 读取一段输入寄存器。
func (m *DataModel) ReadInputRegisters(address, quantity uint16) ([]uint16, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return readRegisters(m.inputRegisters, address, quantity)
}

// WriteSingleCoil 写单个线圈。
func (m *DataModel) WriteSingleCoil(address uint16, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeBits(m.coils, address, []bool{on})
}

// WriteSingleRegister 写单个保持寄存器。
func (m *DataModel) WriteSingleRegister(address, value uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeRegisters(m.holdingRegisters, address, []uint16{value})
}

// WriteMultipleCoils 写多个线圈。
func (m *DataModel) WriteMultipleCoils(address uint16, values []bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeBits(m.coils, address, values)
}

// WriteMultipleRegisters 写多个保持寄存器。
func (m *DataModel) WriteMultipleRegisters(address uint16, values []uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeRegisters(m.holdingRegisters, address, values)
}

// MaskWriteRegister 实现 0x16：结果是 (当前值 AND andMask) OR (orMask AND (NOT andMask))。
func (m *DataModel) MaskWriteRegister(address, andMask, orMask uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkArea(len(m.holdingRegisters), address, 1); err != nil {
		return err
	}
	m.holdingRegisters[address] = (m.holdingRegisters[address] & andMask) | (orMask &^ andMask)
	return nil
}

// ReadWriteMultipleRegisters 实现 0x17：先写一段保持寄存器，再读回读地址的内容。
func (m *DataModel) ReadWriteMultipleRegisters(readAddress, readQuantity, writeAddress uint16, values []uint16) ([]uint16, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := writeRegisters(m.holdingRegisters, writeAddress, values); err != nil {
		return nil, err
	}
	return readRegisters(m.holdingRegisters, readAddress, readQuantity)
}

// --- 应用侧接口：离散输入与输入寄存器只对应用开放（协议侧只读），
// 线圈与保持寄存器也可以由应用直接改。---

// SetCoil 由应用设置一个线圈。
func (m *DataModel) SetCoil(address uint16, value bool) error {
	return m.WriteSingleCoil(address, value)
}

// SetCoils 由应用批量设置线圈。
func (m *DataModel) SetCoils(address uint16, values []bool) error {
	return m.WriteMultipleCoils(address, values)
}

// SetHoldingRegister 由应用设置一个保持寄存器。
func (m *DataModel) SetHoldingRegister(address, value uint16) error {
	return m.WriteSingleRegister(address, value)
}

// SetHoldingRegisters 由应用批量设置保持寄存器。
func (m *DataModel) SetHoldingRegisters(address uint16, values []uint16) error {
	return m.WriteMultipleRegisters(address, values)
}

// SetDiscreteInput 由应用设置一个离散输入。
func (m *DataModel) SetDiscreteInput(address uint16, value bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeBits(m.discreteInputs, address, []bool{value})
}

// SetDiscreteInputs 由应用批量设置离散输入。
func (m *DataModel) SetDiscreteInputs(address uint16, values []bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeBits(m.discreteInputs, address, values)
}

// SetInputRegister 由应用设置一个输入寄存器。
func (m *DataModel) SetInputRegister(address, value uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeRegisters(m.inputRegisters, address, []uint16{value})
}

// SetInputRegisters 由应用批量设置输入寄存器。
func (m *DataModel) SetInputRegisters(address uint16, values []uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return writeRegisters(m.inputRegisters, address, values)
}

// HoldingRegister 由应用读取一个保持寄存器。
func (m *DataModel) HoldingRegister(address uint16) (uint16, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	values, err := readRegisters(m.holdingRegisters, address, 1)
	if err != nil {
		return 0, err
	}
	return values[0], nil
}

// checkArea 校验 [address, address+quantity) 是否落在表的长度内。
func checkArea(length int, address, quantity uint16) error {
	if quantity == 0 || int(address)+int(quantity) > length {
		return exceptionf(ExceptionIllegalDataAddress, "address %d + quantity %d exceeds the area length %d", address, quantity, length)
	}
	return nil
}

func readBits(table []bool, address, quantity uint16) ([]bool, error) {
	if err := checkArea(len(table), address, quantity); err != nil {
		return nil, err
	}
	out := make([]bool, quantity)
	copy(out, table[address:int(address)+int(quantity)])
	return out, nil
}

func writeBits(table []bool, address uint16, values []bool) error {
	if err := checkArea(len(table), address, uint16(len(values))); err != nil {
		return err
	}
	copy(table[address:], values)
	return nil
}

func readRegisters(table []uint16, address, quantity uint16) ([]uint16, error) {
	if err := checkArea(len(table), address, quantity); err != nil {
		return nil, err
	}
	out := make([]uint16, quantity)
	copy(out, table[address:int(address)+int(quantity)])
	return out, nil
}

func writeRegisters(table []uint16, address uint16, values []uint16) error {
	if err := checkArea(len(table), address, uint16(len(values))); err != nil {
		return err
	}
	copy(table[address:], values)
	return nil
}
