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

func (p *Proxy) start(path string, readCh chan<- []byte, writeCh <-chan []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	dev, err := hid.OpenPath(path)
	if err != nil {
		p.logger.Error("HID open error", "err", err)
		return err
	}
	defer func() {
		if err := dev.Close(); err != nil {
			p.logger.Error("HID close error", "err", err)
		}
	}()

	for {
		select {
		case data, ok := <-writeCh:
			if !ok {
				p.logger.Info("HID actor closed")
				return nil
			}

			_, err := dev.Write(data)
			if err != nil {
				p.logger.Error("HID write error", "err", err)
				return err
			}
			p.logger.Debug("HID write", "data", data)
		default:
			buf := make([]byte, 64)
			n, err := dev.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf)
				readCh <- data
				p.logger.Debug("HID read", "data", data)
			}
			if err != nil &&
				!errors.Is(err, io.EOF) {
				p.logger.Error("HID read error", "err", err)
				return err
			}
		}
	}
}

func (p *Proxy) Enumerate() ([]*hid.DeviceInfo, error) {
	devInfos := make([]*hid.DeviceInfo, 0)
	for devInfo, err := range hid.Enumerate() {
		if err != nil {
			p.logger.Error("Device enumeration error", "err", err)
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
	defer func() {
		_ = conn.Close()
	}()

	// Closing readCh will stop hid -> pipe goroutine
	readCh := make(chan []byte)
	// Closing writeCh will close HID goroutine
	writeCh := make(chan []byte)

	// Start device HID actor
	go func() {
		if err := p.start(path, readCh, writeCh); err != nil {
			p.logger.Error("HID actor error", "err", err)
		}
		close(readCh)
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	// pipe -> hid
	go func() {
		defer wg.Done()
		// 64-byte packet + 1 byte for report ID
		buf := make([]byte, 65)
		for {
			// If something went wrong with the pipe, it will lead to closing the HID device
			n, err := conn.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf)
				writeCh <- data
			}
			if err != nil {
				if err != io.EOF {
					p.logger.Error("Pipe -> HID read error", "err", err)
				}
				close(writeCh)
				break
			}
		}
	}()

	// hid -> pipe
	go func() {
		defer wg.Done()
		for {
			// If something went wrong with the device, it will lead to closing the pipe
			data, ok := <-readCh
			if !ok {
				// In hid -> pipe we should close pipe
				_ = conn.Close()
				break
			}

			_, err := conn.Write(data)
			if err != nil {
				p.logger.Error("Pipe write error", "err", err)
				return
			}
		}
	}()

	wg.Wait()
	p.logger.Info("Proxy closed")
}
