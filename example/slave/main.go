// 命令 slave 演示 wemodbus 从站服务端：接在总线上应答真实主站的读写。
//
// 主站（上位机 / PLC / 组态软件）读走这里的数据、写下来开关与设定值；写操作在
// persistHandler 里先落库、再触发业务动作（见 applyCoils）。
//
// 用法：
//
//	go run ./example/slave -port COM3 -unit 1
//	go run ./example/slave -listen :1502 -unit 1                  作为 Modbus TCP 服务端
//	go run ./example/slave -port /dev/ttyUSB0 -unit 1 -mode ascii -lang zh
//
// 数据区固定为：线圈 64、离散输入 64、保持寄存器 128、输入寄存器 64。
// 示例里的输入寄存器模拟量（正弦/余弦 float32，每秒刷新）只是方便演示，真实项目里
// 把 simulate 换成你的数据源即可；保持寄存器 0~7 预置了 1000~1007，线圈与保持寄存器
// 可以由主站改写。
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
	"net"
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
		listen   = flag.String("listen", "", "TCP 监听地址，例如 :1502（与 -port 二选一）")
		unitID   = flag.Int("unit", 1, "本从站地址，0 表示应答任意地址")
		modeName = flag.String("mode", "rtu", "传输模式：rtu / ascii（TCP 下无效）")
		langName = flag.String("lang", "en", "提示语言：en（默认）/ zh")
	)
	flag.Parse()
	wemodbus.SetLanguage(*langName)

	model := wemodbus.NewDataModel(64, 64, 128, 64)
	for i := 0; i < 8; i++ {
		_ = model.SetHoldingRegister(uint16(i), uint16(1000+i))
	}
	_ = model.SetDiscreteInput(0, true)

	handler := newPersistHandler(model)
	go simulate(model)

	if *listen != "" {
		serveTCP(*listen, byte(*unitID), handler)
		return
	}
	if *portName == "" {
		log.Fatal("请用 -port 指定串口，或用 -listen 指定 TCP 监听地址")
	}

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
	}, handler)
	defer server.Close()

	go closeOnSignal(server)

	log.Printf("从站已启动：%s %v，地址 %d，数据区 %v", *portName, server.Config().Mode, *unitID, modelSize(model))
	if err := server.Serve(); err != nil {
		log.Fatalf("从站异常退出：%v", err)
	}
	log.Println("从站已停止")
}

// closeOnSignal 收到 Ctrl+C / SIGTERM 时关闭串口模式的服务端。
func closeOnSignal(server *wemodbus.Server) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("收到退出信号，正在关闭…")
	_ = server.Close()
}

// serveTCP 以 Modbus TCP 服务端方式运行：每个连接一个从站实例，直到监听关闭。
//
// TCP 没有总线概念，unitID 一般填 0（应答任意地址）或某个固定值；要区分多台设备，
// 用不同端口，或让 Handler 按 unitID 查表。TCP 模式下 mode 参数无效。
func serveTCP(address string, unitID byte, handler wemodbus.Handler) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("监听 %s 失败：%v", address, err)
	}
	defer listener.Close()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("收到退出信号，正在关闭…")
		_ = listener.Close()
	}()

	log.Printf("从站服务端已启动（Modbus TCP）：%s，地址 %d", listener.Addr(), unitID)

	var wg sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			server := wemodbus.NewServer(wemodbus.NewTCPTransport(conn), wemodbus.ServerConfig{
				UnitID:  unitID,
				Mode:    wemodbus.ModeTCP,
				Timeout: 5 * time.Second,
			}, handler)
			defer server.Close()

			log.Printf("主站已连接：%s", conn.RemoteAddr())
			_ = server.Serve() // 对端断开时返回，不影响其它连接
			log.Printf("主站已断开：%s", conn.RemoteAddr())
		}()
	}
	wg.Wait()
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
func (h *persistHandler) saveRegister(unitID byte, address, value uint16) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.registers[address] = value
	log.Printf("[持久化] 从站 %d：保持寄存器 %d = %d", unitID, address, value)
	return nil
}

func (h *persistHandler) saveCoil(unitID byte, address uint16, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.coils[address] = on
	log.Printf("[持久化] 从站 %d：线圈 %d = %v", unitID, address, on)
}

// WriteSingleCoil 处理 0x05 写单个线圈。
func (h *persistHandler) WriteSingleCoil(unitID byte, address uint16, on bool) error {
	h.saveCoil(unitID, address, on)
	applyCoils(unitID, address, []bool{on})
	return h.DataModel.WriteSingleCoil(unitID, address, on)
}

// WriteSingleRegister 处理 0x06 写单个保持寄存器：落库成功后才改内存。
func (h *persistHandler) WriteSingleRegister(unitID byte, address, value uint16) error {
	if err := h.saveRegister(unitID, address, value); err != nil {
		log.Printf("[持久化] 从站 %d：寄存器 %d 落库失败：%v", unitID, address, err)
		return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
	}
	return h.DataModel.WriteSingleRegister(unitID, address, value)
}

// WriteMultipleCoils 处理 0x0F 写多个线圈。
func (h *persistHandler) WriteMultipleCoils(unitID byte, address uint16, values []bool) error {
	for i, v := range values {
		h.saveCoil(unitID, address+uint16(i), v)
	}
	applyCoils(unitID, address, values)
	return h.DataModel.WriteMultipleCoils(unitID, address, values)
}

// applyCoils 是「主站下发开关」之后的业务动作入口。
//
// 真实项目里这里是驱动继电器、启停设备、投切电容器、写日志/审计的地方；
// 示例只打印一行日志，演示从站服务端把收到的指令落到业务逻辑上。
func applyCoils(unitID byte, address uint16, values []bool) {
	for i, v := range values {
		state := "断开"
		if v {
			state = "闭合"
		}
		log.Printf("[动作] 从站 %d 线圈 %d：%s", unitID, address+uint16(i), state)
	}
}

// WriteMultipleRegisters 处理 0x10 写多个保持寄存器。
func (h *persistHandler) WriteMultipleRegisters(unitID byte, address uint16, values []uint16) error {
	for i, v := range values {
		if err := h.saveRegister(unitID, address+uint16(i), v); err != nil {
			log.Printf("[持久化] 从站 %d：寄存器 %d 落库失败：%v", unitID, address+uint16(i), err)
			return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
		}
	}
	return h.DataModel.WriteMultipleRegisters(unitID, address, values)
}
