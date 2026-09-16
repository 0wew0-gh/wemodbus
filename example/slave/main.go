// 命令 slave 演示 wemodbus 从站：在串口上模拟一台 Modbus 设备。
//
// 用法：
//
//	go run ./example/slave -port COM3 -unit 1
//	go run ./example/slave -port /dev/ttyUSB0 -unit 1 -mode ascii -lang zh
//
// 数据区固定为：线圈 64、离散输入 64、保持寄存器 128、输入寄存器 64。
// 输入寄存器的前 4 个里放的是正弦/余弦模拟量（两个 float32，每秒刷新），
// 保持寄存器 0~7 预置了 1000~1007，线圈与保持寄存器可以由主站改写。
//
// persistHandler 演示了「感知主站写入并持久化」的做法：在内存数据区外面包一层，
// 主站每次用 0x05 / 0x06 / 0x0F / 0x10 写入时先落库（这里用 map 模拟），成功后再
// 改内存；落库失败就回 0x04「从站设备故障」，内存数据区保持原值。真实项目里把
// saveRegister / saveCoil 换成 SQL、Redis 或文件写入即可。
package main

import (
	"flag"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/0wew0-gh/wemodbus"
)

func main() {
	var (
		portName = flag.String("port", "", "串口名称，例如 COM3 或 /dev/ttyUSB0")
		baudRate = flag.Int("baud", 9600, "波特率")
		unitID   = flag.Int("unit", 1, "本从站地址")
		modeName = flag.String("mode", "rtu", "传输模式：rtu / ascii")
		langName = flag.String("lang", "en", "提示语言：en（默认）/ zh")
	)
	flag.Parse()
	wemodbus.SetLanguage(*langName)

	if *portName == "" {
		log.Fatal("请用 -port 指定串口，例如 -port COM3")
	}

	model := wemodbus.NewDataModel(64, 64, 128, 64)
	for i := 0; i < 8; i++ {
		_ = model.SetHoldingRegister(uint16(i), uint16(1000+i))
	}
	_ = model.SetDiscreteInput(0, true)

	port, err := wemodbus.OpenSerial(wemodbus.SerialConfig{
		PortName: *portName,
		BaudRate: *baudRate,
		DataBits: 8,
		Parity:   wemodbus.ParityNone,
		StopBits: wemodbus.StopBitsOne,
	})
	if err != nil {
		log.Fatalf("打开串口失败：%v", err)
	}

	server := wemodbus.NewServer(port, wemodbus.ServerConfig{
		UnitID:  byte(*unitID),
		Mode:    parseMode(*modeName),
		Timeout: 500 * time.Millisecond,
	}, newPersistHandler(model))
	defer server.Close()

	go simulate(model)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Println("收到退出信号，正在关闭…")
		_ = server.Close()
	}()

	log.Printf("从站已启动：%s %v，地址 %d，数据区 %v", *portName, server.Config().Mode, *unitID, modelSize(model))
	if err := server.Serve(); err != nil {
		log.Fatalf("从站异常退出：%v", err)
	}
	log.Println("从站已停止")
}

// parseMode 把命令行参数转成传输模式。
func parseMode(name string) wemodbus.Mode {
	if name == "ascii" || name == "ASCII" {
		return wemodbus.ModeASCII
	}
	return wemodbus.ModeRTU
}

func modelSize(model *wemodbus.DataModel) [4]int {
	coils, discrete, holding, input := model.Sizes()
	return [4]int{coils, discrete, holding, input}
}

// simulate 每秒刷新输入寄存器里的模拟量：0~1 是正弦、2~3 是余弦，都是 float32。
// 注意这是一次写完整的一组（两个寄存器），避免主站读到半个 float。
func simulate(model *wemodbus.DataModel) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for t := 0.0; ; t += 0.2 {
		sine := float32(math.Sin(t) * 100)
		cosine := float32(math.Cos(t) * 100)
		_ = model.SetInputRegisters(0, wemodbus.Float32ToRegisters(sine, wemodbus.ABCD))
		_ = model.SetInputRegisters(2, wemodbus.Float32ToRegisters(cosine, wemodbus.ABCD))
		<-ticker.C
	}
}

// persistHandler 在内存数据区外面包一层，把主站的写入先落库、再改内存。
//
// 四个写方法必须全部覆盖：只实现 WriteSingleRegister 的话，主站换用 0x10 批量写
// 就绕过持久化了。读请求与未覆盖的方法由嵌入的 *wemodbus.DataModel 兜底。
type persistHandler struct {
	*wemodbus.DataModel
	mu        sync.Mutex
	registers map[uint16]uint16
	coils     map[uint16]bool
}

func newPersistHandler(model *wemodbus.DataModel) *persistHandler {
	return &persistHandler{
		DataModel: model,
		registers: map[uint16]uint16{},
		coils:     map[uint16]bool{},
	}
}

// saveRegister / saveCoil 模拟持久化层：真实项目里换成 SQL、Redis 或文件写入。
// 落库较慢时要注意 Handler 会被 Serve 同步调用，不能超过主站的响应超时。
func (h *persistHandler) saveRegister(address, value uint16) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registers[address] = value
	log.Printf("[持久化] 保持寄存器 %d = %d", address, value)
	return nil
}

func (h *persistHandler) saveCoil(address uint16, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.coils[address] = on
	log.Printf("[持久化] 线圈 %d = %v", address, on)
}

// WriteSingleCoil 处理 0x05 写单个线圈。
func (h *persistHandler) WriteSingleCoil(address uint16, on bool) error {
	h.saveCoil(address, on)
	return h.DataModel.WriteSingleCoil(address, on)
}

// WriteSingleRegister 处理 0x06 写单个保持寄存器：落库成功后才改内存。
func (h *persistHandler) WriteSingleRegister(address, value uint16) error {
	if err := h.saveRegister(address, value); err != nil {
		log.Printf("[持久化] 寄存器 %d 落库失败：%v", address, err)
		return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
	}
	return h.DataModel.WriteSingleRegister(address, value)
}

// WriteMultipleCoils 处理 0x0F 写多个线圈。
func (h *persistHandler) WriteMultipleCoils(address uint16, values []bool) error {
	for i, v := range values {
		h.saveCoil(address+uint16(i), v)
	}
	return h.DataModel.WriteMultipleCoils(address, values)
}

// WriteMultipleRegisters 处理 0x10 写多个保持寄存器。
func (h *persistHandler) WriteMultipleRegisters(address uint16, values []uint16) error {
	for i, v := range values {
		if err := h.saveRegister(address+uint16(i), v); err != nil {
			log.Printf("[持久化] 寄存器 %d 落库失败：%v", address+uint16(i), err)
			return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
		}
	}
	return h.DataModel.WriteMultipleRegisters(address, values)
}
