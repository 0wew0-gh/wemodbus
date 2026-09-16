// 命令 example 演示 wemodbus 库的用法。
//
// 用法：
//
//	go run ./example -list                                        列出本机可用串口
//	go run ./example -dry-run                                     无硬件演示（内存回放响应）
//	go run ./example -port COM3 -unit 1                           读取 2 个保持寄存器
//	go run ./example -port COM3 -unit 1 -quantity 4 -order CDAB   按 CDAB 解释 4 个寄存器
//	go run ./example -port COM3 -unit 1 -address 16 -write 1234   先读后写单个寄存器
//	go run ./example -port COM3 -function 04 -address 1004 -quantity 2  读输入寄存器当 float32
//	go run ./example -port COM3 -function 04 -spec 1004:float32,1006:float32  按清单批量读
//	go run ./example -port COM3 -spec "04:ABCD:4;1004:float32,1006:float32"    按完整清单读取
//	go run ./example -port COM3 -write-spec 1004:27.17:float32                按清单批量写
//	go run ./example -port /dev/ttyUSB0 -mode ascii               使用 ASCII 模式
//
// 从站地址为 0 表示广播：写操作只发不收，读操作会返回 ErrBroadcast。
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0wew0-gh/wemodbus"
)

func main() {
	var (
		portName   = flag.String("port", "", "串口名称，例如 COM3 或 /dev/ttyUSB0")
		baudRate   = flag.Int("baud", 9600, "波特率")
		dataBits   = flag.Int("databits", 8, "数据位")
		parityName = flag.String("parity", "none", "校验位：none / odd / even")
		stopName   = flag.String("stopbits", "1", "停止位：1 / 1.5 / 2")
		unitID     = flag.Int("unit", 1, "从站地址，0 表示广播")
		modeName   = flag.String("mode", "rtu", "传输模式：rtu / ascii")
		timeout    = flag.Duration("timeout", 500*time.Millisecond, "单次响应超时")
		retries    = flag.Int("retries", 2, "失败重试次数")
		gap        = flag.Duration("gap", 0, "帧间静默，0 表示按波特率自动计算")
		address    = flag.Int("address", 0, "寄存器起始地址")
		quantity   = flag.Int("quantity", 2, "读取的寄存器数量")
		orderName  = flag.String("order", "ABCD", "字节序：ABCD / CDAB / BADC / DCBA")
		funcName   = flag.String("function", "03", "读寄存器功能码：03 保持寄存器 / 04 输入寄存器")
		spec       = flag.String("spec", "", "读取清单：1004:float32,1006:float32，或带头的 04:ABCD:4;1004:float32,1006:float32")
		writeSpec  = flag.String("write-spec", "", "写入清单：1004:27.17:float32，或带头的 10:ABCD:4;1004:27.17:float32")
		writeValue = flag.String("write", "", "写入单个保持寄存器的值，留空表示只读")
		showPorts  = flag.Bool("list", false, "列出本机可用串口后退出")
		dryRun     = flag.Bool("dry-run", false, "用内存回放响应，不需要真实串口")
		langName   = flag.String("lang", "en", "提示语言：en（默认）/ zh")
	)
	flag.Parse()
	wemodbus.SetLanguage(*langName)

	if *showPorts {
		if err := printPorts(); err != nil {
			log.Fatalf("列出串口失败：%v", err)
		}
		return
	}

	if *address < 0 || *address > 0xFFFF {
		log.Fatalf("-address 需要在 0~65535 之间，当前为 %d", *address)
	}
	if *quantity < 1 || *quantity > wemodbus.MaxReadRegisters {
		log.Fatalf("-quantity 需要在 1~%d 之间，当前为 %d", wemodbus.MaxReadRegisters, *quantity)
	}
	if *unitID < 0 || *unitID > 0xFF {
		log.Fatalf("-unit 需要在 0~255 之间，当前为 %d", *unitID)
	}

	var write *uint16
	if *writeValue != "" {
		v, err := strconv.ParseUint(*writeValue, 0, 16)
		if err != nil {
			log.Fatalf("-write 需要 0~65535 的整数（支持 0x 前缀）：%v", err)
		}
		w := uint16(v)
		write = &w
	}

	mode, err := parseMode(*modeName)
	if err != nil {
		log.Fatal(err)
	}
	parity, err := parseParity(*parityName)
	if err != nil {
		log.Fatal(err)
	}
	stopBits, err := parseStopBits(*stopName)
	if err != nil {
		log.Fatal(err)
	}
	order, err := parseByteOrder(*orderName)
	if err != nil {
		log.Fatal(err)
	}
	function, err := parseFunction(*funcName)
	if err != nil {
		log.Fatal(err)
	}

	cfg := wemodbus.Config{
		UnitID:          byte(*unitID),
		Mode:            mode,
		Timeout:         *timeout,
		Retries:         *retries,
		InterFrameDelay: *gap,
	}
	serialCfg := wemodbus.SerialConfig{
		PortName: *portName,
		BaudRate: *baudRate,
		DataBits: *dataBits,
		Parity:   parity,
		StopBits: stopBits,
	}

	client, err := connect(*dryRun, serialCfg, cfg)
	if err != nil {
		log.Fatalf("建立连接失败：%v", err)
	}
	defer client.Close()

	if *writeSpec != "" {
		writeValues(client, *writeSpec, order)
		return
	}
	if *spec != "" {
		readSpec(client, function, *spec, order)
		return
	}
	read(client, function, uint16(*address), uint16(*quantity), order, write)
}

// writeValues 写入清单。带分号的完整清单自带功能码与字节序（如
// "10:ABCD:4;1004:27.17:float32,1006:55.16:float32"），否则用 -order。
func writeValues(client *wemodbus.Client, spec string, order wemodbus.ByteOrder) {
	var err error
	if strings.Contains(spec, ";") {
		err = client.WriteBySpec(spec)
	} else {
		err = client.WriteValues(spec, order)
	}
	if err != nil {
		reportError(err)
		os.Exit(1)
	}

	fmt.Println("按清单写入成功：")
	for _, item := range specItems(spec) {
		fmt.Println("  " + item)
	}
}

// readSpec 读取清单。带分号的完整清单自带功能码与字节序（如
// "04:ABCD:4;1004:float32,1006:float32"），否则沿用 -function 与 -order。
func readSpec(client *wemodbus.Client, function byte, spec string, order wemodbus.ByteOrder) {
	var (
		values []float32
		err    error
	)
	full := strings.Contains(spec, ";")
	switch {
	case full:
		values, err = client.ReadBySpec(spec)
	case function == wemodbus.FuncReadInputRegisters:
		values, err = client.ReadInputValues(spec, order)
	default:
		values, err = client.ReadValues(spec, order)
	}
	if err != nil {
		reportError(err)
		os.Exit(1)
	}

	if full {
		fmt.Println("按完整清单读取成功：")
	} else {
		fmt.Printf("按清单读取成功：功能码 0x%02X，字节序 %s\n", function, order)
	}
	items := specItems(spec)
	for i, v := range values {
		if i >= len(items) {
			break
		}
		fmt.Printf("  %-24s = %v\n", items[i], v)
	}
}

// specItems 返回清单里的条目文本：完整清单会先去掉 "功能码:字节序:总长度" 头部。
func specItems(spec string) []string {
	if _, body, ok := strings.Cut(spec, ";"); ok {
		spec = body
	}
	items := strings.Split(spec, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}

// connect 打开真实串口，或用内存回放的 Transport 构造客户端（dry-run）。
func connect(dryRun bool, serialCfg wemodbus.SerialConfig, cfg wemodbus.Config) (*wemodbus.Client, error) {
	if !dryRun {
		if serialCfg.PortName == "" {
			return nil, errors.New("缺少 -port 参数；没有硬件时可用 -dry-run 演示")
		}
		return wemodbus.Open(serialCfg.PortName, serialCfg, cfg)
	}
	fmt.Println("dry-run：使用内存回放的 Transport，不会访问真实串口")
	return wemodbus.NewClient(&replayTransport{mode: cfg.Mode}, cfg), nil
}

// read 读取寄存器（0x03 保持寄存器或 0x04 输入寄存器），按需写入一个保持寄存器，
// 并把错误按类型解释给人看。
func read(client *wemodbus.Client, function byte, address, quantity uint16, order wemodbus.ByteOrder, write *uint16) {
	var (
		registers []uint16
		err       error
	)
	if function == wemodbus.FuncReadInputRegisters {
		registers, err = client.ReadInputRegisters(address, quantity)
	} else {
		registers, err = client.ReadHoldingRegisters(address, quantity)
	}
	if err != nil {
		reportError(err)
		os.Exit(1)
	}

	fmt.Printf("读取寄存器成功：功能码 0x%02X，起始地址 %d，共 %d 个\n", function, address, quantity)
	for i, v := range registers {
		fmt.Printf("  [%d] 0x%04X = %d\n", int(address)+i, v, v)
	}
	if len(registers) >= 2 {
		fmt.Printf("  按 %s 解释：uint32=%d  int32=%d  float32=%v\n", order,
			wemodbus.RegistersToUint32(registers, order),
			wemodbus.RegistersToInt32(registers, order),
			wemodbus.RegistersToFloat32(registers, order))
	}
	if len(registers) >= 4 {
		fmt.Printf("  按 %s 解释：uint64=%d  float64=%v\n", order,
			wemodbus.RegistersToUint64(registers, order),
			wemodbus.RegistersToFloat64(registers, order))
	}

	if write == nil {
		return
	}
	if err := client.WriteSingleRegister(address, *write); err != nil {
		reportError(err)
		os.Exit(1)
	}
	fmt.Printf("写入成功：寄存器 %d = %d\n", address, *write)
}

// reportError 演示如何区分包级错误与从站异常响应。
func reportError(err error) {
	var ex *wemodbus.ExceptionError
	switch {
	case errors.As(err, &ex):
		fmt.Fprintf(os.Stderr, "从站 %d 返回异常：功能码 0x%02X，异常码 0x%02X（%v）\n",
			ex.UnitID, ex.Function, byte(ex.Code), ex.Code)
	case errors.Is(err, wemodbus.ErrBroadcast):
		fmt.Fprintln(os.Stderr, "广播模式（-unit 0）下不能执行读操作")
	case errors.Is(err, wemodbus.ErrTimeout):
		fmt.Fprintln(os.Stderr, "响应超时：请检查串口名称、波特率、从站地址与接线")
	case errors.Is(err, wemodbus.ErrCRC), errors.Is(err, wemodbus.ErrLRC):
		fmt.Fprintln(os.Stderr, "校验失败：链路噪声、波特率不匹配或帧被截断")
	case errors.Is(err, wemodbus.ErrUnitID):
		fmt.Fprintln(os.Stderr, "响应的从站地址与请求不一致：总线上可能有多台设备")
	case errors.Is(err, wemodbus.ErrFunction):
		fmt.Fprintln(os.Stderr, "响应的功能码与请求不一致")
	case errors.Is(err, wemodbus.ErrQuantity):
		fmt.Fprintln(os.Stderr, "请求的地址或数量超出协议允许的范围")
	case errors.Is(err, wemodbus.ErrSpec):
		fmt.Fprintf(os.Stderr, "清单格式有误：%v\n", err)
	case errors.Is(err, wemodbus.ErrFrame):
		fmt.Fprintln(os.Stderr, "响应帧格式非法：可能是噪声或半帧")
	default:
		fmt.Fprintf(os.Stderr, "读写失败：%v\n", err)
	}
}

// printPorts 列出本机可用串口。
func printPorts() error {
	ports, err := wemodbus.Ports()
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		fmt.Println("没有检测到串口")
		return nil
	}
	fmt.Println("可用串口：")
	for _, p := range ports {
		fmt.Println("  " + p)
	}
	return nil
}

func parseMode(name string) (wemodbus.Mode, error) {
	switch name {
	case "rtu", "RTU":
		return wemodbus.ModeRTU, nil
	case "ascii", "ASCII":
		return wemodbus.ModeASCII, nil
	default:
		return 0, fmt.Errorf("-mode 只支持 rtu 或 ascii，当前为 %q", name)
	}
}

func parseParity(name string) (wemodbus.Parity, error) {
	switch name {
	case "none", "N", "n":
		return wemodbus.ParityNone, nil
	case "odd", "O", "o":
		return wemodbus.ParityOdd, nil
	case "even", "E", "e":
		return wemodbus.ParityEven, nil
	default:
		return 0, fmt.Errorf("-parity 只支持 none / odd / even，当前为 %q", name)
	}
}

func parseStopBits(name string) (wemodbus.StopBits, error) {
	switch name {
	case "1", "one":
		return wemodbus.StopBitsOne, nil
	case "1.5", "onepointfive":
		return wemodbus.StopBitsOnePointFive, nil
	case "2", "two":
		return wemodbus.StopBitsTwo, nil
	default:
		return 0, fmt.Errorf("-stopbits 只支持 1 / 1.5 / 2，当前为 %q", name)
	}
}

func parseFunction(name string) (byte, error) {
	switch name {
	case "03", "3", "holding":
		return wemodbus.FuncReadHoldingRegisters, nil
	case "04", "4", "input":
		return wemodbus.FuncReadInputRegisters, nil
	default:
		return 0, fmt.Errorf("-function 只支持 03（保持寄存器）或 04（输入寄存器），当前为 %q", name)
	}
}

func parseByteOrder(name string) (wemodbus.ByteOrder, error) {
	switch name {
	case "ABCD", "abcd":
		return wemodbus.ABCD, nil
	case "CDAB", "cdab":
		return wemodbus.CDAB, nil
	case "BADC", "badc":
		return wemodbus.BADC, nil
	case "DCBA", "dcba":
		return wemodbus.DCBA, nil
	default:
		return 0, fmt.Errorf("-order 只支持 ABCD / CDAB / BADC / DCBA，当前为 %q", name)
	}
}

// replayTransport 是 -dry-run 使用的内存 Transport：收到请求后按请求内容生成一个
// 自洽的响应帧供读取，没有待读数据时返回 (0, nil) 模拟串口读超时。它同时演示了
// 自定义 Transport 需要实现的四个方法。
type replayTransport struct {
	mode    wemodbus.Mode
	pending []byte
}

func (t *replayTransport) Write(p []byte) (int, error) {
	unitID, pdu, err := wemodbus.ParseFrame(t.mode, p)
	if err != nil {
		return 0, err
	}
	response, err := buildResponse(t.mode, unitID, pdu)
	if err != nil {
		return 0, err
	}
	t.pending = response
	return len(p), nil
}

func (t *replayTransport) Read(p []byte) (int, error) {
	if len(t.pending) == 0 {
		return 0, nil
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *replayTransport) Close() error { return nil }

func (t *replayTransport) SetReadTimeout(time.Duration) error { return nil }

// buildResponse 按请求 PDU 造一个合理的响应：读请求返回假数据，写请求按协议回显。
func buildResponse(mode wemodbus.Mode, unitID byte, pdu []byte) ([]byte, error) {
	if len(pdu) < 5 {
		return nil, fmt.Errorf("请求 PDU 太短：% X", pdu)
	}
	function := pdu[0]
	quantity := uint16(pdu[3])<<8 | uint16(pdu[4])

	switch function {
	case wemodbus.FuncReadHoldingRegisters, wemodbus.FuncReadInputRegisters:
		data := make([]byte, 0, 2*int(quantity)+2)
		data = append(data, function, byte(2*quantity))
		for i := uint16(0); i < quantity; i++ {
			v := demoRegister(i)
			data = append(data, byte(v>>8), byte(v))
		}
		return wemodbus.BuildFrame(mode, unitID, data), nil
	case wemodbus.FuncWriteSingleRegister, wemodbus.FuncWriteMultipleRegisters:
		// 写响应的前 5 个字节与请求一致：功能码 + 地址 + 数值（或数量）。
		return wemodbus.BuildFrame(mode, unitID, pdu[:5]), nil
	default:
		return nil, fmt.Errorf("dry-run 不支持功能码 0x%02X", function)
	}
}

// demoRegister 给出回放用的假数据：前两个寄存器是 float32 的 1.5，第 3、4 个
// 是 -2.25，其余按序号递增，方便区分不同的寄存器。
func demoRegister(i uint16) uint16 {
	switch i {
	case 0:
		return 0x3FC0
	case 1:
		return 0x0000
	case 2:
		return 0xC010
	case 3:
		return 0x0000
	default:
		return 0x1000 + i
	}
}
