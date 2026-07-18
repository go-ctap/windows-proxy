package proxy

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ctap/hid"
)

const (
	hidReportSize = 65
	hidPacketSize = 64
	fidoUsagePage = 0xf1d0
	fidoUsage     = 0x01
)

type Proxy struct {
	logger      *slog.Logger
	nextSession atomic.Uint64
	activeMu    sync.Mutex
	active      map[string]struct{}
}

type Option func(*Proxy)

func WithLogger(logger *slog.Logger) Option {
	return func(p *Proxy) {
		p.logger = logger
	}
}

func New(opts ...Option) *Proxy {
	p := &Proxy{
		logger: slog.Default(),
		active: make(map[string]struct{}),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

func (p *Proxy) acquire(path string) bool {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()

	if _, ok := p.active[path]; ok {
		return false
	}

	p.active[path] = struct{}{}
	return true
}

func (p *Proxy) release(path string) {
	p.activeMu.Lock()
	defer p.activeMu.Unlock()

	delete(p.active, path)
}

func (p *Proxy) open(path string) (*hid.Device, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	dev, err := hid.OpenPath(path)
	if err != nil {
		p.logger.Error("HID open error", "err", err)
		return nil, err
	}

	return dev, nil
}

func (p *Proxy) validateDevicePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty HID path")
	}

	for devInfo, err := range hid.Enumerate() {
		if err != nil {
			return "", err
		}

		if devInfo.UsagePage != fidoUsagePage || devInfo.Usage != fidoUsage {
			continue
		}

		if strings.EqualFold(devInfo.Path, path) {
			return devInfo.Path, nil
		}
	}

	return "", fmt.Errorf("HID path is not an enumerated FIDO device")
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}

	return nil
}

func hidPacketLogAttrs(data []byte, hasReportID bool) []any {
	attrs := []any{"bytes", len(data)}

	offset := 0
	if hasReportID {
		offset = 1
	}
	if len(data) >= offset+5 {
		attrs = append(
			attrs,
			"cid", hex.EncodeToString(data[offset:offset+4]),
			"cmd_or_seq", "0x"+hex.EncodeToString(data[offset+4:offset+5]),
		)
	}

	return attrs
}

func (p *Proxy) Enumerate() ([]*hid.DeviceInfo, error) {
	devInfos := make([]*hid.DeviceInfo, 0)
	for devInfo, err := range hid.Enumerate() {
		if err != nil {
			return nil, err
		}

		if devInfo.UsagePage != 0xf1d0 || devInfo.Usage != 0x01 {
			continue
		}

		devInfos = append(devInfos, devInfo)
	}

	return devInfos, nil
}

func (p *Proxy) Proxy(conn net.Conn, requestedPath string) {
	sessionID := p.nextSession.Add(1)
	logger := p.logger.With("proxy_session", sessionID, "requested_path", requestedPath)
	startedAt := time.Now()

	// Make sure client requests access to the FIDO2 device
	path, err := p.validateDevicePath(requestedPath)
	if err != nil {
		logger.Warn("Rejected HID proxy start for non-FIDO path", "err", err)
		_ = conn.Close()
		return
	}
	logger = logger.With("path", path)

	if !p.acquire(path) {
		logger.Warn("Device already has active proxy session")
		_ = conn.Close()
		return
	}
	defer p.release(path)

	dev, err := p.open(path)
	if err != nil {
		_ = conn.Close()
		logger.Info("Proxy closed", "duration", time.Since(startedAt), "reason", "hid_open_error")
		return
	}
	logger.Info("Proxy started")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	closeReason := "unknown"
	var closeOnce sync.Once
	closeProxy := func(reason string) {
		closeOnce.Do(func() {
			closeReason = reason
			cancel()
			close(done)
			_ = conn.Close()
			if err := dev.Close(); err != nil {
				logger.Error("HID close error", "err", err)
			}
		})
	}

	defer func() {
		closeProxy("proxy_complete")
		logger.Info("Proxy closed", "duration", time.Since(startedAt), "reason", closeReason)
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	// pipe -> hid
	go func() {
		defer wg.Done()

		buf := make([]byte, hidReportSize)
		for {
			n, err := io.ReadFull(conn, buf)
			if err != nil {
				select {
				case <-done:
					return
				default:
				}

				if n > 0 {
					logger.Debug("Pipe closed with partial HID report", "bytes", n, "err", err)
					closeProxy("pipe_closed_with_partial_report")
				} else if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					closeProxy("pipe_closed")
				} else {
					logger.Error("Pipe -> HID read error", "err", err)
					closeProxy("pipe_read_error")
				}
				return
			}

			data := make([]byte, len(buf))
			copy(data, buf)

			written, writeErr := dev.Write(ctx, data)
			if writeErr != nil {
				select {
				case <-done:
					return
				default:
				}

				logger.Error("HID write error", "err", writeErr)
				closeProxy("hid_write_error")
				return
			}
			if written != len(data) {
				logger.Error("HID short write", "bytes", written, "want", len(data))
				closeProxy("hid_short_write")
				return
			}

			logger.Debug("HID write", hidPacketLogAttrs(data, true)...)
		}
	}()

	// hid -> pipe
	go func() {
		defer wg.Done()

		for {
			buf := make([]byte, hidPacketSize)
			n, err := dev.Read(ctx, buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])

				if err := writeFull(conn, data); err != nil {
					select {
					case <-done:
						return
					default:
					}

					logger.Error("Pipe write error", "err", err)
					closeProxy("pipe_write_error")
					return
				}

				logger.Debug("HID read", hidPacketLogAttrs(data, false)...)
			}

			if err != nil {
				select {
				case <-done:
					return
				default:
				}

				logger.Error("HID read error", "err", err)
				closeProxy("hid_read_error")
				return
			}
		}
	}()

	wg.Wait()
}
