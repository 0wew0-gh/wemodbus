package wemodbus

import (
	"errors"
	"time"
)

// frameLength 根据帧前缀推算 RTU 帧的总长度。主站用 rtuResponseLength，
// 从站用 rtuRequestLength。TCP 模式由 MBAP 长度域定界，不使用它。
type frameLength func(frame []byte) (int, error)

// frameReader 从 Transport 读取完整的 RTU / ASCII / TCP 帧，主站与从站共用。
//
// 超时由本包自己维护：每次读之前把剩余时间交给 SetReadTimeout，传输层返回
// (0, nil) 时短暂让步后继续判断截止时间，因此不依赖驱动是否上报超时错误。
type frameReader struct {
	t         Transport
	mode      Mode
	timeout   time.Duration
	minPrefix int                // length 所需的最小前缀长度
	length    frameLength        // RTU 帧长推算
	verify    func([]byte) error // 可选 RTU 校验：失败时丢弃首字节重新对准（从站用）
}

// read 读取一个完整帧，超过 timeout 仍未收齐时返回 ErrTimeout。
func (r *frameReader) read() ([]byte, error) {
	deadline := time.Now().Add(r.timeout)
	switch r.mode {
	case ModeASCII:
		return r.readASCII(deadline)
	case ModeTCP:
		return r.readTCP(deadline)
	default:
		return r.readRTU(deadline)
	}
}

// readChunk 从 Transport 读取一次数据。
//
// 返回 (nil, nil) 表示传输层本次没有读到字节（部分串口驱动在超时时返回
// (0, nil)，因此这里不能依赖错误判断超时）；返回 (nil, ErrTimeout) 表示已过截止时间。
func (r *frameReader) readChunk(maxBytes int, deadline time.Time) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fail(ErrFrame, "no room left for more bytes")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, ErrTimeout
	}
	if err := r.t.SetReadTimeout(remaining); err != nil {
		return nil, err
	}
	buf := make([]byte, maxBytes)
	n, err := r.t.Read(buf)
	if err != nil {
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	if n == 0 {
		time.Sleep(zeroReadPause)
		return nil, nil
	}
	return buf[:n], nil
}

// readRTU 先读少量字节推算出整帧长度，再读满。
//
// 设置了 verify 时（从站接收请求），推算失败或校验失败的帧会被逐字节丢弃前缀后
// 重新对准，用来从总线噪声、半帧残余或回显中恢复。
func (r *frameReader) readRTU(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 0, MaxRTUFrameSize)
	for {
		if len(buf) >= r.minPrefix {
			total, err := r.length(buf)
			switch {
			case err != nil && r.verify != nil:
				buf = buf[1:]
				continue
			case err != nil:
				return nil, err
			case len(buf) >= total:
				if r.verify == nil {
					return buf[:total], nil
				}
				if err := r.verify(buf[:total]); err == nil {
					return buf[:total], nil
				}
				buf = buf[1:]
				continue
			}
		}
		if len(buf) >= MaxRTUFrameSize {
			return nil, fail(ErrFrame, "no complete RTU frame within %d bytes", MaxRTUFrameSize)
		}
		chunk, err := r.readChunk(MaxRTUFrameSize-len(buf), deadline)
		if err != nil {
			if errors.Is(err, ErrTimeout) && len(buf) > 0 {
				return nil, fail(ErrTimeout, "incomplete RTU frame, got %d bytes", len(buf))
			}
			return nil, err
		}
		buf = append(buf, chunk...)
	}
}

// readTCP 先读 7 字节 MBAP 头，再按长度域读满整帧。
func (r *frameReader) readTCP(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 0, MaxTCPFrameSize)
	for {
		if len(buf) >= mbapHeaderSize {
			total, err := tcpFrameLength(buf)
			if err != nil {
				return nil, err
			}
			if len(buf) >= total {
				return buf[:total], nil
			}
		}
		if len(buf) >= MaxTCPFrameSize {
			return nil, fail(ErrFrame, "no complete TCP frame within %d bytes", MaxTCPFrameSize)
		}
		chunk, err := r.readChunk(MaxTCPFrameSize-len(buf), deadline)
		if err != nil {
			if errors.Is(err, ErrTimeout) && len(buf) > 0 {
				return nil, fail(ErrTimeout, "incomplete TCP frame, got %d bytes", len(buf))
			}
			return nil, err
		}
		buf = append(buf, chunk...)
	}
}

// readASCII 丢弃帧外字符直到 ':'，再逐字节读到 CRLF。
func (r *frameReader) readASCII(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 0, MaxASCIIFrameSize)
	started := false
	for {
		if len(buf) >= MaxASCIIFrameSize {
			return nil, fail(ErrFrame, "no CRLF within %d bytes", MaxASCIIFrameSize)
		}
		chunk, err := r.readChunk(MaxASCIIFrameSize-len(buf), deadline)
		if err != nil {
			if errors.Is(err, ErrTimeout) && started {
				return nil, fail(ErrTimeout, "incomplete ASCII frame, got %d bytes", len(buf))
			}
			return nil, err
		}
		for _, b := range chunk {
			if !started {
				if b != asciiStart {
					continue
				}
				started = true
			}
			buf = append(buf, b)
			if len(buf) >= 2 && buf[len(buf)-2] == '\r' && buf[len(buf)-1] == '\n' {
				return buf, nil
			}
		}
	}
}
