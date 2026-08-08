package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
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

	"github.com/telesma-app/hid"
)

const (
	hidReportSize            = 65
	hidPacketSize            = 64
	hidInitialDataSize       = 57
	hidContinuationDataSize  = 59
	hidMaxMessageSize        = hidInitialDataSize + 128*hidContinuationDataSize
	proxyNotificationTimeout = 5 * time.Second
	fidoUsagePage            = 0xf1d0
	fidoUsage                = 0x01
)

type Proxy struct {
	logger      *slog.Logger
	nextSession atomic.Uint64
	openDevice  func(string) (hidDevice, error)
	validate    func(string) (string, error)

	mu      sync.Mutex
	devices map[string]*sharedDevice
}

type hidDevice interface {
	Read(context.Context, []byte) (int, error)
	Write(context.Context, []byte) (int, error)
	Close() error
}

type sharedDevice struct {
	proxy  *Proxy
	path   string
	device hidDevice
	logger *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	sessions  map[uint64]net.Conn
}

type Option func(*Proxy)

func WithLogger(logger *slog.Logger) Option {
	return func(p *Proxy) {
		p.logger = logger
	}
}

func New(opts ...Option) *Proxy {
	p := &Proxy{
		logger:  slog.Default(),
		devices: make(map[string]*sharedDevice),
	}
	p.openDevice = func(path string) (hidDevice, error) {
		return p.open(path)
	}
	p.validate = p.validateDevicePath

	for _, opt := range opts {
		opt(p)
	}

	return p
}

func (p *Proxy) acquireDevice(
	path string,
	sessionID uint64,
	conn net.Conn,
) (*sharedDevice, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if shared := p.devices[path]; shared != nil {
		shared.sessions[sessionID] = conn

		return shared, nil
	}

	device, err := p.openDevice(path)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	shared := &sharedDevice{
		proxy:    p,
		path:     path,
		device:   device,
		logger:   p.logger.With("path", path),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
		sessions: map[uint64]net.Conn{sessionID: conn},
	}
	p.devices[path] = shared
	go shared.readReports()

	return shared, nil
}

func (p *Proxy) releaseDevice(path string, sessionID uint64, shared *sharedDevice) {
	p.mu.Lock()
	current := p.devices[path]
	if current != shared {
		p.mu.Unlock()

		return
	}

	delete(shared.sessions, sessionID)
	if len(shared.sessions) != 0 {
		p.mu.Unlock()

		return
	}

	delete(p.devices, path)
	p.mu.Unlock()

	shared.close()
}

func (p *Proxy) failDevice(path string, shared *sharedDevice, err error) {
	p.mu.Lock()
	if p.devices[path] != shared {
		p.mu.Unlock()

		return
	}

	delete(p.devices, path)
	connections := make([]net.Conn, 0, len(shared.sessions))
	for _, conn := range shared.sessions {
		connections = append(connections, conn)
	}
	clear(shared.sessions)
	p.mu.Unlock()

	if err != nil && !errors.Is(err, context.Canceled) {
		shared.logger.Error("Shared HID endpoint failed", "err", err)
	}
	shared.stop()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func (d *sharedDevice) stop() {
	d.closeOnce.Do(func() {
		d.cancel()
		if err := d.device.Close(); err != nil {
			d.logger.Error("HID close error", "err", err)
		}
	})
}

func (d *sharedDevice) close() {
	d.stop()
	<-d.done
}

func (d *sharedDevice) readReports() {
	defer close(d.done)

	for {
		buf := make([]byte, hidPacketSize)
		n, err := d.device.Read(d.ctx, buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			d.broadcast(data)
			d.logger.Debug("HID read", hidPacketLogAttrs(data, false)...)
		}
		if err != nil {
			d.proxy.failDevice(d.path, d, err)

			return
		}
	}
}

func (d *sharedDevice) broadcast(data []byte) {
	d.proxy.mu.Lock()
	if d.proxy.devices[d.path] != d {
		d.proxy.mu.Unlock()

		return
	}

	connections := make([]net.Conn, 0, len(d.sessions))
	for _, conn := range d.sessions {
		connections = append(connections, conn)
	}
	d.proxy.mu.Unlock()

	for _, conn := range connections {
		if err := conn.SetWriteDeadline(time.Now().Add(proxyNotificationTimeout)); err != nil {
			_ = conn.Close()

			continue
		}
		err := writeFull(conn, data)
		_ = conn.SetWriteDeadline(time.Time{})
		if err != nil {
			_ = conn.Close()
		}
	}
}

func (d *sharedDevice) writeReports(ctx context.Context, reports [][]byte) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	for _, report := range reports {
		written, err := d.device.Write(ctx, report)
		if err != nil {
			return err
		}
		if written != len(report) {
			return io.ErrShortWrite
		}

		d.logger.Debug("HID write", hidPacketLogAttrs(report, true)...)
	}

	return nil
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

func readHIDRequest(reader io.Reader) ([][]byte, error) {
	first := make([]byte, hidReportSize)
	if _, err := io.ReadFull(reader, first); err != nil {
		return nil, err
	}
	if first[5]&0x80 == 0 {
		return nil, errors.New("CTAPHID request starts with a continuation packet")
	}

	payloadLength := int(binary.BigEndian.Uint16(first[6:8]))
	if payloadLength > hidMaxMessageSize {
		return nil, fmt.Errorf("CTAPHID payload length %d exceeds maximum", payloadLength)
	}

	remaining := max(0, payloadLength-hidInitialDataSize)
	continuations := (remaining + hidContinuationDataSize - 1) / hidContinuationDataSize
	reports := make([][]byte, 1, continuations+1)
	reports[0] = first

	for sequence := 0; sequence < continuations; sequence++ {
		report := make([]byte, hidReportSize)
		if _, err := io.ReadFull(reader, report); err != nil {
			return nil, err
		}
		if !bytes.Equal(report[1:5], first[1:5]) || report[5] != byte(sequence) {
			return nil, errors.New("invalid CTAPHID continuation packet")
		}

		reports = append(reports, report)
	}

	return reports, nil
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
	path, err := p.validate(requestedPath)
	if err != nil {
		logger.Warn("Rejected HID proxy start for non-FIDO path", "err", err)
		_ = conn.Close()
		return
	}
	logger = logger.With("path", path)

	shared, err := p.acquireDevice(path, sessionID, conn)
	if err != nil {
		_ = conn.Close()
		logger.Info("Proxy closed", "duration", time.Since(startedAt), "reason", "hid_open_error")
		return
	}
	defer p.releaseDevice(path, sessionID, shared)
	logger.Info("Proxy started")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closeReason := "unknown"
	defer func() {
		_ = conn.Close()
		logger.Info("Proxy closed", "duration", time.Since(startedAt), "reason", closeReason)
	}()

	for {
		reports, readErr := readHIDRequest(conn)
		if readErr != nil {
			switch {
			case errors.Is(readErr, io.EOF):
				closeReason = "pipe_closed"
			case errors.Is(readErr, io.ErrUnexpectedEOF):
				closeReason = "pipe_closed_with_partial_request"
			default:
				logger.Error("Pipe -> HID read error", "err", readErr)
				closeReason = "pipe_read_error"
			}

			return
		}
		if writeErr := shared.writeReports(ctx, reports); writeErr != nil {
			logger.Error("HID write error", "err", writeErr)
			closeReason = "hid_write_error"
			p.failDevice(path, shared, writeErr)

			return
		}
	}
}
