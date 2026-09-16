# wemodbus

`github.com/0wew0-gh/wemodbus` 是一个 Modbus **主站（客户端）** 库，支持 **RTU** 与 **ASCII**
两种串行传输模式，API 为强类型 Go 风格，不依赖 JSON 指令协议，也不做 cgo 导出。

## 支持范围

| 功能码 | 方法 | 说明 |
| --- | --- | --- |
| 0x01 | `ReadCoils` | 读线圈 |
| 0x02 | `ReadDiscreteInputs` | 读离散输入 |
| 0x03 | `ReadHoldingRegisters` | 读保持寄存器 |
| 0x04 | `ReadInputRegisters` | 读输入寄存器 |
| 0x05 | `WriteSingleCoil` | 写单个线圈 |
| 0x06 | `WriteSingleRegister` | 写单个寄存器 |
| 0x0F | `WriteMultipleCoils` | 写多个线圈 |
| 0x10 | `WriteMultipleRegisters` | 写多个寄存器 |
| 0x16 | `MaskWriteRegister` | 掩码写寄存器 |
| 0x17 | `ReadWriteMultipleRegisters` | 一次事务先写后读 |

不在范围内：Modbus TCP、从站实现、HTTP 接口层、旧 JSON 指令协议。

## 快速开始

```go
package main

import (
    "log"
    "time"

    "github.com/0wew0-gh/wemodbus"
)

func main() {
    client, err := wemodbus.Open("COM3", wemodbus.SerialConfig{
        BaudRate: 9600,
        DataBits: 8,
        Parity:   wemodbus.ParityNone,
        StopBits: wemodbus.StopBitsOne,
    }, wemodbus.Config{
        UnitID:  1,
        Timeout: 500 * time.Millisecond,
        Retries: 2,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    registers, err := client.ReadHoldingRegisters(0x0000, 2)
    if err != nil {
        log.Fatal(err)
    }
    value := wemodbus.RegistersToFloat32(registers, wemodbus.ABCD)
    log.Println(value)

    if err := client.WriteSingleRegister(0x0010, 1234); err != nil {
        log.Fatal(err)
    }
}
```

模块路径与包名同名：导入 `github.com/0wew0-gh/wemodbus` 后以 `wemodbus.` 前缀调用。

`Open` 会打开串口；如果需要复用已有的字节通道（测试、虚拟串口、其它链路），
用 `NewClient(transport, cfg)` 自行提供 `Transport`。

## 运行示例

`example/` 下有一个可直接运行的示例程序，覆盖串口参数、读写、字节序与错误处理：

```powershell
go run ./example -list                                    # 列出本机可用串口
go run ./example -dry-run                                 # 内存回放响应，无需硬件
go run ./example -port COM3 -unit 1 -quantity 4           # 读 4 个保持寄存器
go run ./example -port COM3 -unit 1 -address 16 -write 1234
go run ./example -port COM3 -function 04 -address 1004 -quantity 2 -order ABCD   # 读输入寄存器当 float32
go run ./example -port COM3 -function 04 -spec 1004:float32,1006:float32          # 按清单批量读
go run ./example -port COM3 -write-spec 1004:27.17:float32                       # 按清单批量写
go run ./example -port /dev/ttyUSB0 -mode ascii -order CDAB
```

`-dry-run` 用内存 Transport 回放预置响应帧，没有硬件也能跑通完整流程；它同时
演示了如何为 `NewClient` 实现自定义 `Transport`。`go run ./example -h` 可以看到
全部参数。

## ASCII 模式

```go
client, err := wemodbus.Open("COM3", sc, wemodbus.Config{
    UnitID: 1,
    Mode:   wemodbus.ModeASCII,
})
```

RTU 与 ASCII 的帧构造、校验（CRC16 / LRC）与解析由同一个 `Client` 自动处理，
业务代码无需区分。

## 字节序

多寄存器数值的字节序用 `ByteOrder` 表达，四种取值都是自逆的：

| 取值 | 32 位示例（值 `0x12345678`） | 说明 |
| --- | --- | --- |
| `ABCD` | `1234 5678` | 标准大端 |
| `CDAB` | `5678 1234` | 交换两个寄存器 |
| `BADC` | `3412 7856` | 寄存器内交换字节 |
| `DCBA` | `7856 3412` | 整体反序（等价小端） |

```go
value, err := client.ReadFloat32(0x0000, wemodbus.CDAB)
err = client.WriteUint32(0x0010, 0x12345678, wemodbus.ABCD)
```

需要读写输入寄存器（0x04）时，请直接用 `ReadInputRegisters` 配合
`value.go` 中的 `RegistersToUint32` / `RegistersToFloat32` 等函数。

## 按清单批量读写

`ReadValues` / `ReadInputValues` 接收 `地址:类型` 的清单字符串，一次读回多组
数值，统一返回 `[]float32`（读几个寄存器由类型决定）：

```go
// 输入寄存器 1004、1006 各是一个 float32（各占 2 个寄存器）
values, err := client.ReadInputValues("1004:float32,1006:float32", wemodbus.ABCD)
// values == []float32{27.17, 55.16}
```

支持的类型与占用的寄存器数：

| 类型 | 寄存器数 |
| --- | --- |
| `uint16` `int16` | 1 |
| `uint32` `int32` `float32` | 2 |
| `uint64` `int64` `float64` | 4 |

写入用 `WriteValues`，格式是 `地址:数值:类型`，第二段换成要写入的数值：

```go
// 27.17 写到保持寄存器 1004、1005，55.16 写到 1006、1007
err := client.WriteValues("1004:27.17:float32,1006:55.16:float32", wemodbus.ABCD)
```

整数类型支持 `0x` 前缀的十六进制写法。地址首尾相接的条目在读写两侧都会合并成
一次总线请求（读单次不超过 125 个寄存器、写不超过 123 个；写只覆盖一个寄存器
时用 0x06，多个时用 0x10）。返回值的顺序始终与清单一致，清单无法解析时返回
`ErrSpec`。`ReadValues` 走保持寄存器（0x03），`ReadInputValues` 走输入寄存器
（0x04）；示例程序支持用 `-spec` / `-write-spec` 直接验证（见上面的「运行示例」）。

### 带头部的完整清单

把功能码、字节序、总长度也写进字符串时，用 `ReadBySpec` / `WriteBySpec`：

```go
// 04 = 输入寄存器，ABCD = 字节序，4 = 从首个地址起连续读 4 个寄存器
values, err := client.ReadBySpec("04:ABCD:4;1004:float32,1006:float32")
// values == []float32{27.17, 55.16}

// 10 = 写多个寄存器
err = client.WriteBySpec("10:ABCD:4;1004:27.17:float32,1006:55.16:float32")
```

- 第一部分是 `功能码:字节序[:总长度]`：功能码按十六进制写（读 `03` / `04`，
  写 `06` / `10`），字节序取 `ABCD` / `CDAB` / `BADC` / `DCBA`，总长度可省略。
- 给出总长度时整段只发一次总线请求，并要求条目首尾相接、恰好铺满这一段寄存器：
  `04:ABCD:4;1004:float32,1007:float32` 会因为第二项应从 1006 开始而返回 `ErrSpec`，
  错误文本会指出是哪一项、应为哪个地址。
- 省略总长度时各条目分别读写。

## 错误处理

包级错误都可以用 `errors.Is` 判断（只列出一部分）：

| 错误 | 含义 |
| --- | --- |
| `ErrTimeout` | 超时未收齐完整响应 |
| `ErrCRC` / `ErrLRC` | 校验失败 |
| `ErrFrame` | 帧格式非法或写类回显不匹配 |
| `ErrUnitID` / `ErrFunction` | 响应地址或功能码与请求不符 |
| `ErrQuantity` | 数量为 0 或超出协议上限 |
| `ErrClosed` / `ErrBroadcast` | 客户端已关闭 / 广播下执行读操作 |

从站返回的异常响应会转换成 `*ExceptionError`：

```go
registers, err := client.ReadHoldingRegisters(0xFFFF, 2)
var ex *wemodbus.ExceptionError
if errors.As(err, &ex) {
    log.Printf("从站 %d 功能码 0x%02X 异常：%v", ex.UnitID, ex.Function, ex.Code)
}
```

## 从站（slave）

`Server` 让本库也能扮演 Modbus 设备：从 `Transport` 读请求，交给 `Handler` 处理，再写回应答。`DataModel` 是一个现成的内存数据区实现。

```go
model := wemodbus.NewDataModel(64, 64, 128, 64) // 线圈 / 离散输入 / 保持寄存器 / 输入寄存器
_ = model.SetHoldingRegister(0, 1234)
_ = model.SetInputRegisters(0, wemodbus.Float32ToRegisters(27.17, wemodbus.ABCD))

port, err := wemodbus.OpenSerial(wemodbus.SerialConfig{PortName: "COM3", BaudRate: 9600, DataBits: 8})
if err != nil {
    log.Fatal(err)
}
server := wemodbus.NewServer(port, wemodbus.ServerConfig{UnitID: 1}, model)
defer server.Close()
log.Fatal(server.Serve()) // 阻塞处理请求，直到 Close
```

- 支持功能码 `01/02/03/04/05/06/0F/10`；非法功能码回 `0x01`，越界地址回 `0x02`，数量或数据值非法回 `0x03`，处理器返回普通 error 回 `0x04`。
- 广播（地址 0）的请求会被执行但不应答；地址不符的帧直接忽略。
- 总线噪声、半帧残余与回显造成的错位，由「长度推算 + CRC 校验 + 逐字节重同步」处理。
- 要接自己的后端就实现 `Handler` 接口；返回 `wemodbus.Exception(code)` 可以指定回给主站的异常码。
- `Server.Stats()` 给出请求 / 响应 / 异常 / 忽略的累计计数。

### 数据区与地址

`NewDataModel(coils, discreteInputs, holdingRegisters, inputRegisters)` 的四个参数是**四张表的长度**，顺序固定；四张表各自从 0 开始编号，地址空间互相独立：

| 参数 | 表 | `NewDataModel(64, 64, 128, 64)` 的合法地址 | 相关功能码 |
| --- | --- | --- | --- |
| 1 | 线圈 | 0~63 | `0x01` 读，`0x05` / `0x0F` 写 |
| 2 | 离散输入 | 0~63 | `0x02` 读（只读） |
| 3 | 保持寄存器 | 0~127 | `0x03` 读，`0x06` / `0x10` / `0x16` 写 |
| 4 | 输入寄存器 | 0~63 | `0x04` 读（只读） |

同一个地址数字落在哪张表，由主站发的功能码决定——主站用 `0x03` 读地址 5 和用 `0x01` 读地址 5 是两个互不相干的位置。数据区本身不关心“指令”，它只是四张表，功能码由 `Server` 分派到 `Handler` 的对应方法。

**主站读一个地址时从站怎么回**，以下是示例：

以 `0x03` 读地址 1000 为例，结果取决于数据区里有没有这个地址：

```text
数据区只有 128 个保持寄存器（0~127）：
  请求：01 03 03 E8 00 01 04 7A    起始地址 1000(0x03E8)，数量 1
  响应：01 83 02 C0 F1             0x83 = 0x03|0x80，异常码 0x02 非法数据地址

数据区有 1100 个保持寄存器（0~1099），1000 处是 0x1234：
  请求：01 03 03 E8 00 01 04 7A
  响应：01 03 02 12 34 B5 33       字节数 02，数据 0x1234
```

- 成功响应是「功能码 + 字节数 + 数量 × 2 字节数据」，异常响应是「功能码 | 0x80 + 异常码」。
- 越界（`address + quantity > 表长`）只有从站能判断，回 `0x02`；而数量超出协议上限（一次读 125 个寄存器、写 123 个）在主站侧就被本地拦下，不会发到总线上。

**想让从站模拟手册里的地址**，把对应的表建够长即可：

```go
// 模拟一台“输入寄存器从 1004 起有 float32”的设备
model := wemodbus.NewDataModel(0, 0, 0, 1010) // 输入寄存器 0~1009
_ = model.SetInputRegisters(1004, wemodbus.Float32ToRegisters(27.17, wemodbus.ABCD))
_ = model.SetInputRegisters(1006, wemodbus.Float32ToRegisters(55.16, wemodbus.ABCD))
```

前面空着的地址只是占位（一个寄存器 2 字节，1000 个约 2KB）。**如果地址很稀疏**（只用到 1000、2000、30000 这类），可以自己实现 `Handler` 用 map 存值，地址没登记时回 `Exception(ExceptionIllegalDataAddress)`。

注意手册编号与协议地址的区别：`41001`、`31004` 是 Modicon 传统编号，换算成协议地址分别是 `1000`、`1003`（减 40001 / 减 30001）。主站发到总线上的、以及数据区里使用的，都是这个从 0 开始的协议地址。

`example/slave` 是一个可直接运行的设备模拟器：

```powershell
go run ./example/slave -port COM3 -unit 1
```

它把正弦、余弦波写进输入寄存器 0~3（两个 float32，每秒刷新），保持寄存器 0~7 预置为 1000~1007。启动后可以用主站读它：

```powershell
go run ./example -port COM3 -function 04 -spec "04:ABCD:4;0:float32,2:float32"
```

示例里的 `persistHandler` 还演示了**感知主站写入并持久化**的做法：在 `DataModel` 外面包一层实现 `Handler`，主站用 `0x05` / `0x06` / `0x0F` / `0x10` 写入时先落库（示例用 map 模拟）再改内存，落库失败回 `0x04` 并保持内存原值。四个写方法都要覆盖——只实现 `WriteSingleRegister` 的话，主站换用 `0x10` 批量写就绕过持久化了。

### 写入持久化（钩子示例）

`Handler` 就是感知主站写入的钩子：从站收到写请求时必然调用你实现的对应方法，没有额外的事件订阅 API。把 `DataModel` 包一层即可一边维护内存数据区、一边落库：

```go
type persistHandler struct {
    *wemodbus.DataModel // 读请求与未覆盖的方法由它兜底
    db *sql.DB          // 换成 SQL、Redis 或文件都可以
}

// 0x06 写单个保持寄存器：先落库，成功后再改内存
func (h persistHandler) WriteSingleRegister(address, value uint16) error {
    if _, err := h.db.Exec("UPDATE regs SET value = ? WHERE addr = ?", value, address); err != nil {
        return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure) // 主站收到 0x04
    }
    return h.DataModel.WriteSingleRegister(address, value)
}

// 0x10 写多个保持寄存器：一个事务里写完，再改内存
func (h persistHandler) WriteMultipleRegisters(address uint16, values []uint16) error {
    tx, err := h.db.Begin()
    if err != nil {
        return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
    }
    for i, v := range values {
        if _, err := tx.Exec("UPDATE regs SET value = ? WHERE addr = ?", v, address+uint16(i)); err != nil {
            _ = tx.Rollback()
            return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
        }
    }
    if err := tx.Commit(); err != nil {
        return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
    }
    return h.DataModel.WriteMultipleRegisters(address, values)
}

// 0x05 写单个线圈
func (h persistHandler) WriteSingleCoil(address uint16, on bool) error {
    if _, err := h.db.Exec("UPDATE coils SET value = ? WHERE addr = ?", on, address); err != nil {
        return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
    }
    return h.DataModel.WriteSingleCoil(address, on)
}

// 0x0F 写多个线圈
func (h persistHandler) WriteMultipleCoils(address uint16, values []bool) error {
    for i, v := range values {
        if _, err := h.db.Exec("UPDATE coils SET value = ? WHERE addr = ?", v, address+uint16(i)); err != nil {
            return wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)
        }
    }
    return h.DataModel.WriteMultipleCoils(address, values)
}
```

接进从站时把处理器换掉即可：

```go
server := wemodbus.NewServer(port, wemodbus.ServerConfig{UnitID: 1}, persistHandler{DataModel: model, db: db})
```

几点必须注意：

- **四个写方法都要实现**（`0x05` / `0x06` / `0x0F` / `0x10`）；启用了 `0x16` / `0x17` 的可选接口时也要一并覆盖。
- **顺序是先落库、再改内存**。反过来写会在数据库失败时造成“内存是新值、库里是旧值”，而主站收到的是异常。
- **失败要返回异常**：`wemodbus.Exception(wemodbus.ExceptionSlaveDeviceFailure)` 让主站收到 `0x04`，`ExceptionIllegalDataValue` 对应 `0x03`，以此类推。
- **Handler 是同步调用的**，它在 `Serve` 循环里执行，落库耗时不能超过主站的响应超时；确实慢的话可以“先写内存 + 投递队列异步入库”，但要接受主站收到成功时数据尚未落盘。
- **不驻留内存也可以**：如果数据本来就以数据库为准，自己实现全部 8 个 `Handler` 方法、直接读写数据库即可，这时不需要 `DataModel`。

## Modbus TCP

把 `Mode` 设成 `ModeTCP`，主站与从站就走 MBAP 报文（事务标识 2 + 协议标识 2 + 长度 2 + 单元标识 1 + PDU）——没有 CRC / LRC，帧长由长度域给出。

```go
// 主站
transport, err := wemodbus.OpenTCP("192.168.1.10:502", 3*time.Second)
if err != nil {
    log.Fatal(err)
}
client := wemodbus.NewClient(transport, wemodbus.Config{UnitID: 1, Mode: wemodbus.ModeTCP, Timeout: time.Second})

// 从站
listener, _ := net.Listen("tcp", ":502")
conn, _ := listener.Accept()
server := wemodbus.NewServer(wemodbus.NewTCPTransport(conn), wemodbus.ServerConfig{UnitID: 1, Mode: wemodbus.ModeTCP}, model)
go func() { log.Fatal(server.Serve()) }()
```

- 主站每次请求自动分配递增的事务标识，响应的事务标识与请求不符时按 `ErrFrame` 处理。
- 从站把请求的事务标识原样回填；单元标识 0 仍是广播（执行但不应答）。
- 测试里可以用 `net.Pipe()` 配 `NewTCPTransport` 把主站与从站在内存中对接，本仓库的 `tcp_test.go` 就是这么做的。
- TCP 下 `InterFrameDelay` 没有意义，`Client` 的帧间静默逻辑只对 RTU 生效。

## 提示语言

库返回的错误提示支持英文（默认）与简体中文，用包级 `SetLanguage` 切换，对所有
Client 生效：

```go
wemodbus.SetLanguage("zh")   // 中文提示
wemodbus.SetLanguage("en")   // 英文提示（默认）
```

语言码不区分大小写，`zh-CN`、`zh_Hans`、`zh-TW` 这类写法都按简体中文处理，无法
识别的值按英文处理；`GetLanguage()` 返回当前语言码。切换语言只影响错误文本，
`errors.Is` / `errors.As` 的判断与语言无关：

```go
wemodbus.SetLanguage("zh")
_, err := client.ReadHoldingRegisters(0, 1)
if errors.Is(err, wemodbus.ErrTimeout) {
    log.Println(err) // wemodbus: 响应超时
}
```

两点例外：`ExceptionCode.String()` 遵循 Go 的 Stringer 惯例保持英文（`ExceptionError.Error()` 跟随语言）；串口驱动自身返回的错误（例如 `Serial port busy`）来自底层库，不受本开关影响。示例程序可以用 `-lang` 试：

```powershell
go run ./example -dry-run -lang zh -spec "04:ABCD:4;1004:float32,1007:float32"
```

## 超时、重试与广播

- **超时**：`Config.Timeout`，零值取 500ms。本库自行维护截止时间，不依赖
  串口驱动的超时错误；Windows 上 `go.bug.st/serial` 超时返回 `(0, nil)` 也不会
  造成永不超时的空转。
- **重试**：`Config.Retries` 表示额外尝试次数，共 `Retries+1` 次。超时、CRC/LRC
  失败、帧格式错误、地址与功能码不匹配、写类回显不匹配都会重试；从站异常默认
  不重试，只有 0x05（确认）与 0x06（忙）会重试；传输层写失败不重试。
- **广播**：`Config.UnitID` 为 0 时只发送不接收，写操作按 `InterFrameDelay`
  等待从站处理，读操作返回 `ErrBroadcast`。
- **帧间静默**：RTU 需要至少 3.5 个字符时间。`Open` 在 `InterFrameDelay` 为 0
  时按波特率自动计算（9600 约 4ms）；`NewClient` 无法得知波特率，需要时请显式
  设置，或使用 `wemodbus.RTUFrameDelay(baudRate)`。

## 并发

串行链路是半双工总线，`Client` 内部用互斥锁串行化事务，因此同一个 `Client`
可以被多个 goroutine 并发使用。要真正并行访问多个从站，请为每个串口建立独立
的 `Client`。

## 测试

```powershell
$env:GOFLAGS='-mod=mod'
go test .
```

`client_test.go` 使用内存 `Transport`，写入时回放预置响应、缓冲为空时返回
`(0, nil)`，与 Windows 串口驱动超时后的行为一致，用来验证本库自己维护的截止
时间确实生效。
