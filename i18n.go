package wemodbus

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// 内部语言码。
const (
	langEN = "en"
	langZH = "zh"
)

var (
	langMu   sync.RWMutex
	langCode = langEN
)

// SetLanguage 设置错误提示的语言，全局生效。
//
// 支持 "en"（英文，默认）与 "zh"（简体中文）；语言码不区分大小写，也接受
// "zh-CN"、"zh-Hans"、"zh_TW" 这类带地区或下划线的写法。无法识别的值按英文处理。
//
// 只影响错误提示的文本，errors.Is / errors.As 的判断与语言无关。
func SetLanguage(lang string) {
	langMu.Lock()
	defer langMu.Unlock()
	langCode = normalizeLanguage(lang)
}

// GetLanguage 返回当前语言码："en" 或 "zh"。
func GetLanguage() string {
	langMu.RLock()
	defer langMu.RUnlock()
	return langCode
}

// normalizeLanguage 把各种语言写法归一化为内部语言码，未知语言按英文处理。
func normalizeLanguage(lang string) string {
	tag := strings.ToLower(strings.TrimSpace(lang))
	tag = strings.ReplaceAll(tag, "_", "-")
	if strings.HasPrefix(tag, "zh") {
		return langZH
	}
	return langEN
}

// localizedError 是按当前语言生成的提示，同时保留哨兵错误供 errors.Is 判断。
type localizedError struct {
	sentinel error
	text     string
}

func (e *localizedError) Error() string { return e.text }

func (e *localizedError) Unwrap() error { return e.sentinel }

// isEnglish 报告当前是否为英文：英文下直接沿用哨兵错误本身的文本。
func isEnglish() bool {
	langMu.RLock()
	defer langMu.RUnlock()
	return langCode == langEN
}

// message 按当前语言格式化一条提示。format 用英文书写并作为翻译表的键，
// 缺少译文时原样返回，因此新增提示可以只写英文。
func message(format string, args ...interface{}) string {
	langMu.RLock()
	lang := langCode
	langMu.RUnlock()

	if lang != langEN {
		if text, ok := translations[lang][format]; ok {
			format = text
		}
	}
	return fmt.Sprintf(format, args...)
}

// errorText 返回哨兵错误在当前语言下的描述（不含 wemodbus 前缀）。
func errorText(err error) string {
	var s *sentinel
	if errors.As(err, &s) {
		if isEnglish() {
			return s.en
		}
		return s.zh
	}
	return strings.TrimPrefix(err.Error(), "wemodbus: ")
}

// fail 构造一条绑定哨兵错误的本地化提示，errors.Is / errors.As 照常可用。
// format 为空时直接返回哨兵，此时它的文本已经跟随语言。
func fail(sentinel error, format string, args ...interface{}) error {
	if format == "" {
		return sentinel
	}
	detail := message(format, args...)
	if isEnglish() {
		return fmt.Errorf("%w: %s", sentinel, detail)
	}
	return &localizedError{
		sentinel: sentinel,
		text:     "wemodbus: " + errorText(sentinel) + ": " + detail,
	}
}

// errorWrap 构造一条包装底层错误的本地化提示。
func errorWrap(err error, format string, args ...interface{}) error {
	return fmt.Errorf("%s: %w", "wemodbus: "+message(format, args...), err)
}

// sentinelTexts 是每个包级错误在两种语言下的描述。包级错误自身的文本固定为英文，
// 作为 errors.Is 的哨兵；对外返回的文本由这里按当前语言生成。
// translations 保存各语言的提示表：键是代码里写的英文格式串（含占位符），
// 值是译文。缺条目时退回英文。
var translations = map[string]map[string]string{
	langZH: zhMessages,
}

// zhMessages 是简体中文提示表。
var zhMessages = map[string]string{
	// CRC / LRC
	"%d bytes is too short to carry a CRC":  "%d 字节太短，装不下一个 CRC",
	"%d bytes is too short to carry an LRC": "%d 字节太短，装不下一个 LRC",
	"got 0x%04X, want 0x%04X":               "实际 0x%04X，期望 0x%04X",
	"got 0x%02X, want 0x%02X":               "实际 0x%02X，期望 0x%02X",

	// 事务与读取
	"write failed":    "写入失败",
	"got %d, want %d": "实际 %d，期望 %d",
	"request PDU of %d bytes cannot be echoed":  "请求 PDU 只有 %d 字节，无法回显校验",
	"no room left for more bytes":               "缓冲区已满，无法继续读取",
	"no complete RTU frame within %d bytes":     "%d 字节内没有收到完整的 RTU 帧",
	"incomplete RTU frame, got %d bytes":        "RTU 帧不完整，只收到 %d 字节",
	"no CRLF within %d bytes":                   "%d 字节内没有收到 CRLF",
	"incomplete ASCII frame, got %d bytes":      "ASCII 帧不完整，只收到 %d 字节",
	"read %d registers from address %d failed":  "读取地址 %d 起 %d 个寄存器失败",
	"write %d registers from address %d failed": "写入地址 %d 起 %d 个寄存器失败",

	// PDU 与数量校验
	"%d not in [%d, %d]": "%d 不在 [%d, %d] 范围内",
	"address 0x%04X + quantity %d exceeds address space": "地址 0x%04X 加数量 %d 超出了地址空间",
	"empty response PDU":                       "响应 PDU 为空",
	"truncated exception response":             "异常响应被截断",
	"truncated read response":                  "读响应被截断",
	"response declares %d data bytes, want %d": "响应声明 %d 个数据字节，期望 %d",
	"response carries %d data bytes, want %d":  "响应实际带 %d 个数据字节，期望 %d",
	"echo response is %d bytes, want %d":       "回显响应 %d 字节，期望 %d",
	"echo mismatch: got % X, want % X":         "回显不匹配：实际 % X，期望 % X",

	// 帧格式
	"PDU of %d bytes exceeds %d":                                       "PDU %d 字节，超过上限 %d",
	"RTU frame of %d bytes is too short":                               "RTU 帧只有 %d 字节，太短",
	"RTU frame of %d bytes exceeds %d":                                 "RTU 帧 %d 字节，超过上限 %d",
	"ASCII frame of %d bytes exceeds %d":                               "ASCII 帧 %d 字节，超过上限 %d",
	"ASCII frame must start with %q":                                   "ASCII 帧必须以 %q 开始",
	"ASCII frame must end with CRLF":                                   "ASCII 帧必须以 CRLF 结束",
	"ASCII frame body must be at least 3 bytes of hex digits in pairs": "ASCII 帧体至少要有 3 字节成对的十六进制字符",
	"RTU response header of %d bytes is too short":                     "RTU 响应头只有 %d 字节，太短",
	"RTU response declares %d data bytes":                              "RTU 响应声明了 %d 个数据字节",
	"unsupported function code 0x%02X":                                 "不支持的功能码 0x%02X",

	// 清单
	"spec is empty":        "清单为空",
	"item %d is empty":     "第 %d 项为空",
	"item %d %q is not %s": "第 %d 项 %q 不是 %s",
	"item %d: address %q is not an integer in 0~65535":        "第 %d 项地址 %q 不是 0~65535 的整数",
	"item %d: type %q is not supported (available: %s)":       "第 %d 项类型 %q 不支持（可用 %s）",
	"item %d: %s value %q cannot be parsed":                   "第 %d 项 %s 的值 %q 无法解析",
	"type %q is not supported":                                "不支持的类型 %q",
	"function 06 can only write 1 register, got %d":           "功能码 06 只能写 1 个寄存器，实际要写 %d 个",
	"function 06 can only write 1 register, item %d takes %d": "功能码 06 只能写 1 个寄存器，第 %d 项占 %d 个",

	// 带头部的完整清单
	"read spec function code must be 03 (holding) or 04 (input), got 0x%02X":    "读取清单的功能码只能是 03（保持寄存器）或 04（输入寄存器），当前为 0x%02X",
	"write spec function code must be 06 (single) or 10 (multiple), got 0x%02X": "写入清单的功能码只能是 06（写单个）或 10（写多个），当前为 0x%02X",
	"total length %d exceeds the read limit %d":                                 "总长度 %d 超过一次读取的上限 %d",
	"total length %d exceeds the write limit %d":                                "总长度 %d 超过一次写入的上限 %d",
	"missing semicolon, a full spec looks like 04:ABCD:4;1004:float32":          "缺少分号，完整清单形如 04:ABCD:4;1004:float32",
	"missing function:order[:total] before semicolon":                           "分号前缺少 功能码:字节序[:总长度]",
	"missing items after semicolon":                                             "分号后缺少条目",
	"a full spec can contain only one semicolon":                                "完整清单只能有一个分号",
	"head %q is not function:order[:total]":                                     "第一部分 %q 不是 功能码:字节序[:总长度]",
	"total length %q is not a positive integer":                                 "总长度 %q 不是正整数",
	"function code %q is not hexadecimal":                                       "功能码 %q 不是十六进制数",
	"function code 0x%02X is not supported (read: 03/04, write: 06/10)":         "功能码 0x%02X 不支持（读用 03/04，写用 06/10）",
	"byte order %q is not supported (available: ABCD / CDAB / BADC / DCBA)":     "字节序 %q 不支持（可用 ABCD / CDAB / BADC / DCBA）",
	"spec has no items": "清单没有条目",
	"item %d address %d does not match the declared total: the spec starts at %d and covers %d registers, so this item should start at %d": "第 %d 项的地址 %d 与预设总长度不一致：清单声明从 %d 起共 %d 个寄存器，该项应从 %d 开始",
	"items cover %d registers (%d~%d), but the declared total is %d":                                                                       "条目共覆盖 %d 个寄存器（%d~%d），与预设总长度 %d 不一致",

	// 从站
	"RTU request of %d bytes is too short":                "RTU 请求只有 %d 字节，太短",
	"RTU request declares %d data bytes":                  "RTU 请求声明了 %d 个数据字节",
	"empty request PDU":                                   "请求 PDU 为空",
	"unexpected function code 0x%02X":                     "意外的功能码 0x%02X",
	"request PDU is %d bytes, want %d":                    "请求 PDU %d 字节，期望 %d",
	"coil value must be 0xFF00 or 0x0000, got 0x%04X":     "线圈值必须是 0xFF00 或 0x0000，实际为 0x%04X",
	"malformed request PDU for function 0x%02X":           "功能码 0x%02X 的请求 PDU 格式非法",
	"request declares %d data bytes, got %d":              "请求声明 %d 个数据字节，实际 %d",
	"quantity %d needs %d data bytes, got %d":             "数量 %d 需要 %d 个数据字节，实际 %d",
	"quantity is zero":                                    "数量为 0",
	"address %d + quantity %d exceeds the area length %d": "地址 %d 加数量 %d 超出了数据区长度 %d",

	// Modbus TCP
	"TCP frame of %d bytes is too short":    "TCP 报文只有 %d 字节，太短",
	"TCP header of %d bytes is too short":   "TCP 报文头只有 %d 字节，太短",
	"unexpected protocol id 0x%04X":         "协议标识 0x%04X 不是 0",
	"MBAP declares %d bytes, got %d":        "MBAP 声明 %d 字节，实际 %d 字节",
	"MBAP declares %d bytes":                "MBAP 声明的长度 %d 非法",
	"no complete TCP frame within %d bytes": "%d 字节内没有收到完整的 TCP 报文",
	"incomplete TCP frame, got %d bytes":    "TCP 报文不完整，只收到 %d 字节",
	"unexpected transaction id %d, want %d": "事务标识 %d 与请求的 %d 不一致",

	// 从站异常
	"exception 0x%02X (%s)":                                  "异常 0x%02X（%s）",
	"exception 0x%02X (%s) from unit %d for function 0x%02X": "异常 0x%02X（%s）：从站 %d，功能码 0x%02X",
	"unknown exception 0x%02X":                               "未知异常 0x%02X",
}

// 异常码描述。
var exceptionTexts = map[ExceptionCode]struct{ en, zh string }{
	ExceptionIllegalFunction:         {"illegal function", "非法功能码"},
	ExceptionIllegalDataAddress:      {"illegal data address", "非法数据地址"},
	ExceptionIllegalDataValue:        {"illegal data value", "非法数据值"},
	ExceptionSlaveDeviceFailure:      {"slave device failure", "从站设备故障"},
	ExceptionAcknowledge:             {"acknowledge", "从站已确认"},
	ExceptionSlaveDeviceBusy:         {"slave device busy", "从站忙"},
	ExceptionMemoryParityError:       {"memory parity error", "存储器奇偶校验错误"},
	ExceptionGatewayPathUnavailable:  {"gateway path unavailable", "网关路径不可用"},
	ExceptionGatewayTargetNoResponse: {"gateway target device failed to respond", "网关目标设备无响应"},
}

// text 返回异常码在当前语言下的描述。
func (c ExceptionCode) text() string {
	texts, ok := exceptionTexts[c]
	if !ok {
		return message("unknown exception 0x%02X", byte(c))
	}
	if isEnglish() {
		return texts.en
	}
	return texts.zh
}
