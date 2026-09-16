package wemodbus

import (
	"errors"
	"fmt"
)

// sentinel 是包级哨兵错误：Error() 的文本按当前语言生成，errors.Is 通过指针
// 身份匹配，因此切换语言不影响判断结果。
type sentinel struct {
	en string
	zh string
}

func (s *sentinel) Error() string {
	if isEnglish() {
		return "wemodbus: " + s.en
	}
	return "wemodbus: " + s.zh
}

// 包级错误。调用方应使用 errors.Is 判断；文本按 SetLanguage 设置的语言生成。
var (
	// ErrTimeout 表示在 Config.Timeout 内没有收齐一个完整响应。
	ErrTimeout = &sentinel{en: "response timeout", zh: "响应超时"}
	// ErrCRC 表示 RTU 帧的 CRC16 校验失败。
	ErrCRC = &sentinel{en: "CRC check failed", zh: "CRC 校验失败"}
	// ErrLRC 表示 ASCII 帧的 LRC 校验失败。
	ErrLRC = &sentinel{en: "LRC check failed", zh: "LRC 校验失败"}
	// ErrFrame 表示帧格式非法：长度不足、长度与声明不符、缺少起始符、
	// 十六进制字符非法、回显内容与请求不一致等。
	ErrFrame = &sentinel{en: "malformed frame", zh: "帧格式非法"}
	// ErrUnitID 表示响应中的从站地址与请求不一致。
	ErrUnitID = &sentinel{en: "unexpected unit id", zh: "从站地址与请求不一致"}
	// ErrFunction 表示响应中的功能码与请求不一致，且不是异常响应。
	ErrFunction = &sentinel{en: "unexpected function code", zh: "功能码与请求不一致"}
	// ErrQuantity 表示请求的线圈/寄存器数量为 0 或超出协议上限。
	ErrQuantity = &sentinel{en: "invalid quantity", zh: "数量非法"}
	// ErrClosed 表示客户端已经关闭。
	ErrClosed = &sentinel{en: "client is closed", zh: "客户端已关闭"}
	// ErrBroadcast 表示广播（UnitID 为 0）下不能执行读操作。
	ErrBroadcast = &sentinel{en: "read is not allowed in broadcast mode", zh: "广播模式下不能执行读操作"}
	// ErrSpec 表示 "地址:长度:类型" 形式的寄存器清单无法解析或不自洽。
	ErrSpec = &sentinel{en: "invalid register spec", zh: "寄存器清单非法"}
)

// ExceptionCode 是从站异常响应的异常码。
type ExceptionCode byte

// 协议定义的异常码。
const (
	ExceptionIllegalFunction         ExceptionCode = 0x01
	ExceptionIllegalDataAddress      ExceptionCode = 0x02
	ExceptionIllegalDataValue        ExceptionCode = 0x03
	ExceptionSlaveDeviceFailure      ExceptionCode = 0x04
	ExceptionAcknowledge             ExceptionCode = 0x05
	ExceptionSlaveDeviceBusy         ExceptionCode = 0x06
	ExceptionMemoryParityError       ExceptionCode = 0x08
	ExceptionGatewayPathUnavailable  ExceptionCode = 0x0A
	ExceptionGatewayTargetNoResponse ExceptionCode = 0x0B
)

// String 返回异常码的英文说明（实现 fmt.Stringer）。需要本地化文本时用
// ExceptionError.Error()，它按 SetLanguage 设置的语言输出。
func (c ExceptionCode) String() string {
	if texts, ok := exceptionTexts[c]; ok {
		return texts.en
	}
	return fmt.Sprintf("unknown exception 0x%02X", byte(c))
}

// ExceptionError 是从站返回的异常响应（功能码最高位置 1），也用于从站侧表示
// 处理器要回给主站的异常。
type ExceptionError struct {
	// UnitID 是响应该请求的从站地址。
	UnitID byte
	// Function 是原始请求的功能码，不含 0x80 标志位。
	Function byte
	// Code 是异常码。
	Code ExceptionCode
	// detail 是本地生成的说明（例如数据区越界的地址范围），来自从站处理器。
	detail string
}

// Error 实现 error 接口，文本按当前语言（SetLanguage）生成。从站侧本地生成的
// 异常（UnitID 与 Function 均为 0，例如数据区越界）没有请求上下文，只给出异常码
// 与说明；来自从站响应的异常则带上地址与功能码。
func (e *ExceptionError) Error() string {
	var text string
	if e.UnitID == 0 && e.Function == 0 {
		text = "wemodbus: " + message("exception 0x%02X (%s)", byte(e.Code), e.Code.text())
	} else {
		text = "wemodbus: " + message("exception 0x%02X (%s) from unit %d for function 0x%02X",
			byte(e.Code), e.Code.text(), e.UnitID, e.Function)
	}
	if e.detail != "" {
		text += ": " + e.detail
	}
	return text
}

// exceptionf 用指定异常码构造一条带说明的错误，供从站处理器（含 DataModel）
// 返回给 Server：从站会把它转成对应的异常响应，说明只用于日志。
func exceptionf(code ExceptionCode, format string, args ...interface{}) error {
	return &ExceptionError{Code: code, detail: message(format, args...)}
}

// Is 让 errors.Is 可以按异常码匹配，例如
// errors.Is(err, Exception(ExceptionIllegalDataAddress))。
func (e *ExceptionError) Is(target error) bool {
	var other *ExceptionError
	if errors.As(target, &other) {
		return e.Code == other.Code
	}
	return false
}

// retryable 判断一次失败的事务是否值得重试。
//
// 超时、校验失败、帧格式错误、地址/功能码不匹配都重试；从站异常默认不重试，
// 只有「确认」(0x05) 与「从站忙」(0x06) 属于临时状态才重试。
// 传输层自身的写失败不属于临时故障，不重试。
func retryable(err error) bool {
	var ex *ExceptionError
	if errors.As(err, &ex) {
		return ex.Code == ExceptionAcknowledge || ex.Code == ExceptionSlaveDeviceBusy
	}
	return errors.Is(err, ErrTimeout) ||
		errors.Is(err, ErrCRC) ||
		errors.Is(err, ErrLRC) ||
		errors.Is(err, ErrFrame) ||
		errors.Is(err, ErrUnitID) ||
		errors.Is(err, ErrFunction)
}
