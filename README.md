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
