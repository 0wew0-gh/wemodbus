package wemodbus

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// withLanguage 在测试期间切换语言，结束后恢复英文。
func withLanguage(t *testing.T, lang string) {
	t.Helper()
	previous := GetLanguage()
	SetLanguage(lang)
	t.Cleanup(func() { SetLanguage(previous) })
}

func TestLanguageDefaultsToEnglish(t *testing.T) {
	if got := GetLanguage(); got != "en" {
		t.Fatalf("默认语言 = %q, want en", got)
	}
	if text := ErrTimeout.Error(); !strings.Contains(text, "response timeout") {
		t.Fatalf("默认错误文本 = %q, want 英文", text)
	}
}

func TestSetLanguageNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"en", "en"},
		{"EN", "en"},
		{"en-US", "en"},
		{"zh", "zh"},
		{"ZH", "zh"},
		{"zh-CN", "zh"},
		{"zh_cn", "zh"},
		{"zh-Hans", "zh"},
		{"zh-TW", "zh"},
		{"  zh  ", "zh"},
		{"fr", "en"},
		{"", "en"},
		{"de-DE", "en"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			withLanguage(t, "en")
			SetLanguage(tt.input)
			if got := GetLanguage(); got != tt.want {
				t.Fatalf("SetLanguage(%q) -> %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestErrorTextFollowsLanguage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		en   string
		zh   string
	}{
		{"超时", ErrTimeout, "response timeout", "响应超时"},
		{"CRC", ErrCRC, "CRC check failed", "CRC 校验失败"},
		{"地址", ErrUnitID, "unexpected unit id", "从站地址与请求不一致"},
		{"广播", ErrBroadcast, "read is not allowed in broadcast mode", "广播模式下不能执行读操作"},
		{"清单", ErrSpec, "invalid register spec", "寄存器清单非法"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withLanguage(t, "en")
			if got := tt.err.Error(); !strings.Contains(got, tt.en) {
				t.Fatalf("英文下 = %q, want 含 %q", got, tt.en)
			}
			SetLanguage("zh")
			if got := tt.err.Error(); !strings.Contains(got, tt.zh) {
				t.Fatalf("中文下 = %q, want 含 %q", got, tt.zh)
			}
		})
	}
}

func TestReturnedErrorsFollowLanguage(t *testing.T) {
	// 库返回的错误（而不是包级变量本身）也要跟着语言走。
	withLanguage(t, "zh")

	c, _ := newTestClient(t, Config{UnitID: 0x01, Timeout: 20 * time.Millisecond})
	_, err := c.ReadHoldingRegisters(0, 1)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if !strings.Contains(err.Error(), "响应超时") {
		t.Fatalf("超时文本 = %q, want 中文", err.Error())
	}

	// 清单校验错误同样是中文。
	_, err = c.ReadBySpec("04:ABCD:4;1004:float32,1007:float32")
	if !errors.Is(err, ErrSpec) {
		t.Fatalf("err = %v, want ErrSpec", err)
	}
	if !strings.Contains(err.Error(), "预设总长度不一致") {
		t.Fatalf("清单错误文本 = %q, want 中文", err.Error())
	}
}

func TestErrorsIsWorksInEveryLanguage(t *testing.T) {
	// 本地化不应破坏 errors.Is / errors.As。
	withLanguage(t, "en")
	c, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadHoldingRegisters|0x80, byte(ExceptionIllegalDataAddress)))
	_, err := c.ReadHoldingRegisters(0, 1)
	var ex *ExceptionError
	if !errors.As(err, &ex) || ex.Code != ExceptionIllegalDataAddress {
		t.Fatalf("英文下 errors.As 失败：%v", err)
	}
	if !strings.Contains(err.Error(), "illegal data address") {
		t.Fatalf("英文异常文本 = %q", err.Error())
	}

	SetLanguage("zh")
	c2, _ := newTestClient(t, Config{UnitID: 0x01}, rtuReplyFunc(0x01, FuncReadHoldingRegisters|0x80, byte(ExceptionIllegalDataAddress)))
	_, err = c2.ReadHoldingRegisters(0, 1)
	if !errors.As(err, &ex) || ex.Code != ExceptionIllegalDataAddress {
		t.Fatalf("中文下 errors.As 失败：%v", err)
	}
	if !strings.Contains(err.Error(), "非法数据地址") {
		t.Fatalf("中文异常文本 = %q", err.Error())
	}
}

func TestExceptionCodeStringStaysEnglish(t *testing.T) {
	// String() 遵循 Go 惯例保持英文，Error() 才随语言变化。
	withLanguage(t, "zh")
	if got := ExceptionIllegalFunction.String(); got != "illegal function" {
		t.Fatalf("ExceptionCode.String() = %q, want 英文", got)
	}
	if got := ExceptionCode(0x7F).String(); !strings.Contains(got, "unknown exception") {
		t.Fatalf("未知异常码 String() = %q", got)
	}
	ex := &ExceptionError{UnitID: 1, Function: FuncReadHoldingRegisters, Code: ExceptionSlaveDeviceBusy}
	if got := ex.Error(); !strings.Contains(got, "从站忙") {
		t.Fatalf("ExceptionError.Error() = %q, want 含中文异常描述", got)
	}
}

func TestLocalizedErrorsKeepSentinelChain(t *testing.T) {
	withLanguage(t, "zh")
	err := fail(ErrQuantity, "%d not in [%d, %d]", 300, 1, 125)
	if !errors.Is(err, ErrQuantity) {
		t.Fatalf("errors.Is(err, ErrQuantity) = false，err = %v", err)
	}
	if !strings.Contains(err.Error(), "数量非法") || !strings.Contains(err.Error(), "300") {
		t.Fatalf("文本 = %q，want 含中文描述与参数", err.Error())
	}

	// 英文下沿用哨兵错误本身，保持原有文本形态。
	SetLanguage("en")
	err = fail(ErrQuantity, "%d not in [%d, %d]", 300, 1, 125)
	if !errors.Is(err, ErrQuantity) {
		t.Fatalf("英文下 errors.Is 失败：%v", err)
	}
	if !strings.Contains(err.Error(), "invalid quantity: 300 not in [1, 125]") {
		t.Fatalf("英文文本 = %q", err.Error())
	}
}

func TestLanguageSwitchIsConcurrencySafe(t *testing.T) {
	// 语言是全局状态，读写都加锁，这里做一次竞态冒烟测试。
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			SetLanguage("zh")
			SetLanguage("en")
		}
	}()
	for i := 0; i < 500; i++ {
		_ = ErrTimeout.Error()
		_ = message("response timeout")
		_ = errorText(ErrSpec)
	}
	<-done
	SetLanguage("en")
}
