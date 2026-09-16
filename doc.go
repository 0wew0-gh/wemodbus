// Package wemodbus 实现 Modbus RTU / ASCII 主站（客户端）。
//
// # 模式与帧格式
//
// RTU 帧为「地址 1 字节 + PDU + CRC16 2 字节」，CRC 低字节在前，帧之间需要
// 至少 3.5 个字符时间的静默。ASCII 帧为「':' + 地址与 PDU 的十六进制文本 +
// LRC 2 个字符 + CRLF」，帧界明确，不需要额外的静默时间。
//
// 支持的功能码：
//
//	0x01 读线圈            0x02 读离散输入
//	0x03 读保持寄存器      0x04 读输入寄存器
//	0x05 写单个线圈        0x06 写单个寄存器
//	0x0F 写多个线圈        0x10 写多个寄存器
//	0x16 掩码写寄存器      0x17 读写多个寄存器
//
// # 超时
//
// 本包不使用 io.ReadFull，而是自己维护截止时间：每次读之前把剩余时间交给
// Transport.SetReadTimeout，并在返回 (0, nil) 时短暂让步后继续判断截止时间。
// 这样即使底层串口驱动在超时时返回 (0, nil)（Windows 上的 go.bug.st/serial
// 就是如此），也不会出现永不超时的空转。超过 Config.Timeout 仍未收齐完整
// 帧时返回 ErrTimeout。
//
// # 重试
//
// 一次事务最多尝试 Config.Retries+1 次。超时、CRC/LRC 校验失败、帧格式错误、
// 地址或功能码不匹配都会重试；从站异常（*ExceptionError）默认不重试，只有
// 0x05「确认」与 0x06「从站忙」这两种临时状态会重试；传输层写失败不重试。
//
// # 并发
//
// 串行链路是半双工总线，Client 内部用互斥锁把事务串行化，因此同一个 Client
// 可以被多个 goroutine 并发调用，但任一时刻只有一个事务在链路上。若需要
// 真正的并行，请为每个串口各建一个 Client。
//
// # 广播
//
// Config.UnitID 为 0 表示广播：只发送不接收，写操作按 Config.InterFrameDelay
// 等待从站处理，读操作直接返回 ErrBroadcast。
//
// # 提示语言
//
// 错误提示默认英文，用 SetLanguage("zh") 可切换为简体中文，对所有 Client 生效；
// 文本变化不影响 errors.Is / errors.As 的判断。串口驱动自身返回的错误不受影响。
//
// # 快速开始
//
//	client, err := wemodbus.Open("COM3", wemodbus.SerialConfig{
//		BaudRate: 9600,
//		DataBits: 8,
//		Parity:   wemodbus.ParityNone,
//		StopBits: wemodbus.StopBitsOne,
//	}, wemodbus.Config{UnitID: 1, Timeout: 500 * time.Millisecond})
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer client.Close()
//
//	registers, err := client.ReadHoldingRegisters(0, 2)
//	if err != nil {
//		log.Fatal(err)
//	}
//	value := wemodbus.RegistersToFloat32(registers, wemodbus.ABCD)
package wemodbus
