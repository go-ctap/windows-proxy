package proxy

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"runtime"
	"sync"

	"github.com/go-ctap/hid"
)

type Proxy struct {
	logger *slog.Logger
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
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
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

func (p *Proxy) Proxy(conn net.Conn, path string) {
	dev, err := p.open(path)
	if err != nil {
		_ = conn.Close()
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var closeOnce sync.Once
	closeResources := func() {
		closeOnce.Do(func() {
			if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				p.logger.Error("Pipe close error", "err", err)
			}
			if err := dev.Close(); err != nil {
				p.logger.Error("HID close error", "err", err)
			}
		})
	}

	// pipe -> hid
	go func() {
		defer wg.Done()

		buf := make([]byte, 65)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])

				_, writeErr := dev.Write(data)
				if writeErr != nil {
					p.logger.Error("HID write error", "err", writeErr)
					closeResources()
					return
				}

				p.logger.Debug("HID write", "data", data)
			}

			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					p.logger.Error("Pipe -> HID read error", "err", err)
				}
				closeResources()
				return
			}
		}
	}()

	// hid -> pipe
	go func() {
		defer wg.Done()

		for {
			buf := make([]byte, 64)
			n, err := dev.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])

				if err := writeFull(conn, data); err != nil {
					p.logger.Error("Pipe write error", "err", err)
					closeResources()
					return
				}

				p.logger.Debug("HID read", "data", data)
			}

			if err != nil {
				if !errors.Is(err, io.EOF) {
					p.logger.Error("HID read error", "err", err)
				}
				closeResources()
				return
			}
		}
	}()

	wg.Wait()
	closeResources()
	p.logger.Info("Proxy closed")
}
