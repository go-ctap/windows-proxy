package transport

import (
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/telesma-app/windows-proxy/internal/domain"
	"github.com/telesma-app/windows-proxy/pkg/proxy"
	proxyprotocol "github.com/telesma-app/windows-proxy/protocol"

	"github.com/Microsoft/go-winio"
	"github.com/fxamacker/cbor/v2"
)

type pipeDelivery struct {
	logger *slog.Logger
	config *Config
	proxy  *proxy.Proxy

	mu           sync.Mutex
	listener     net.Listener
	connections  map[net.Conn]struct{}
	subscribers  map[net.Conn]chan struct{}
	stopping     bool
	wg           sync.WaitGroup
	shutdownOnce sync.Once
	shutdownErr  error
}

const (
	controlTimeout           = 10 * time.Second
	notificationWriteTimeout = 5 * time.Second
)

func NewDelivery(logger *slog.Logger, config *Config, p *proxy.Proxy) domain.Delivery {
	d := &pipeDelivery{
		logger:      logger,
		config:      config,
		proxy:       p,
		connections: make(map[net.Conn]struct{}),
		subscribers: make(map[net.Conn]chan struct{}),
	}

	return d
}

func pipeSecurityDescriptor(config *Config) string {
	var b strings.Builder

	// DACL:
	// - deny all access for network users
	// - allow full access to Administrators
	// - allow full access to Local System
	// - deny FILE_CREATE_PIPE_INSTANCE for Everyone
	// - optionally allow read/write to authenticated users for legacy clients
	b.WriteString(`D:(D;OICI;GA;;;S-1-5-2)`)
	b.WriteString(`(A;OICI;GA;;;S-1-5-32-544)`)
	b.WriteString(`(A;OICI;GA;;;S-1-5-18)`)
	b.WriteString(`(D;OICI;0x4;;;S-1-1-0)`)
	if config.AllowAuthenticatedUsers {
		b.WriteString(`(A;OICI;GRGW;;;S-1-5-11)`)
	}
	for _, sid := range config.AllowedClientSIDs {
		sid = strings.TrimSpace(sid)
		if sid == "" {
			continue
		}
		b.WriteString(`(A;OICI;GRGW;;;`)
		b.WriteString(sid)
		b.WriteString(`)`)
	}

	return b.String()
}

func (d *pipeDelivery) Listen() (net.Listener, error) {
	addr := proxyprotocol.NamedPipePath
	if d.config.Debug {
		addr = d.config.Address
	}
	d.logger.Info("Listening transport requests.", "addr", addr)

	if d.config.Debug {
		return net.Listen("tcp", d.config.Address)
	}

	return winio.ListenPipe(addr, &winio.PipeConfig{
		MessageMode:        true,
		SecurityDescriptor: pipeSecurityDescriptor(d.config),
	})
}

func (d *pipeDelivery) Serve(l net.Listener) error {
	d.mu.Lock()
	if d.stopping {
		d.mu.Unlock()
		_ = l.Close()
		return nil
	}
	if d.listener != nil {
		d.mu.Unlock()
		return errors.New("transport is already serving")
	}
	d.listener = l
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		if d.listener == l {
			d.listener = nil
		}
		d.mu.Unlock()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			d.mu.Lock()
			stopping := d.stopping
			d.mu.Unlock()
			if stopping || errors.Is(err, net.ErrClosed) {
				d.logger.Debug("Pipe listener closed")
				return nil
			}
			d.logger.Error("Pipe accept error", "err", err)
			continue
		}
		d.logger.Debug("Accepted pipe connection")

		d.mu.Lock()
		if d.stopping {
			d.mu.Unlock()
			_ = conn.Close()
			continue
		}
		d.connections[conn] = struct{}{}
		d.wg.Add(1)
		d.mu.Unlock()

		go func() {
			defer func() {
				d.mu.Lock()
				delete(d.connections, conn)
				d.mu.Unlock()
				d.wg.Done()
			}()
			d.handleConn(conn)
		}()
	}
}

func (d *pipeDelivery) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(controlTimeout)); err != nil {
		d.logger.Error("Set control deadline error", "err", err)
		return
	}

	msg, err := proxyprotocol.ParseMessage(conn)
	if err != nil {
		d.logger.Error("Parse message error", "err", err)
		return
	}

	switch msg.Command {
	case proxyprotocol.CommandEnumerate:
		devInfos, err := d.proxy.Enumerate()
		if err != nil {
			d.logger.Error("Device enumeration error", "err", err)
			return
		}

		msg, err := proxyprotocol.NewMessage(proxyprotocol.CommandEnumerate, devInfos)
		if err != nil {
			d.logger.Error("NewMessage error", "err", err)
			return
		}

		if _, err := msg.WriteTo(conn); err != nil {
			d.logger.Error("WriteTo error", "err", err)
			return
		}

		d.logger.Info("Enumerate response sent")
	case proxyprotocol.CommandStart:
		var path string
		if err := cbor.Unmarshal(msg.Data, &path); err != nil {
			d.logger.Error("Unmarshal error", "err", err)
			return
		}
		if err := conn.SetDeadline(time.Time{}); err != nil {
			d.logger.Error("Clear control deadline error", "err", err)
			return
		}

		d.logger.Debug("Start command received", "path", path)
		d.proxy.Proxy(conn, path)
	case proxyprotocol.CommandDevicesChanged:
		if err := conn.SetDeadline(time.Time{}); err != nil {
			d.logger.Error("Clear control deadline error", "err", err)
			return
		}

		notifications := make(chan struct{}, 1)
		d.mu.Lock()
		d.subscribers[conn] = notifications
		d.mu.Unlock()
		defer func() {
			d.mu.Lock()
			delete(d.subscribers, conn)
			d.mu.Unlock()
		}()

		clientClosed := make(chan struct{})
		go func() {
			defer close(clientClosed)
			var b [1]byte
			_, _ = conn.Read(b[:])
		}()
		defer func() {
			_ = conn.Close()
			<-clientClosed
		}()

		select {
		case notifications <- struct{}{}:
		default:
		}
		for {
			select {
			case <-notifications:
				if err := d.writeDevicesChanged(conn); err != nil {
					d.logger.Debug("Device event subscriber closed", "err", err)
					return
				}
			case <-clientClosed:
				return
			}
		}
	default:
		d.logger.Error("Unknown command", "command", msg.Command)
	}
}

func (d *pipeDelivery) writeDevicesChanged(conn net.Conn) error {
	message, err := proxyprotocol.NewMessage(proxyprotocol.CommandDevicesChanged, nil)
	if err != nil {
		return err
	}

	if err := conn.SetWriteDeadline(time.Now().Add(notificationWriteTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()

	_, err = message.WriteTo(conn)
	return err
}

func (d *pipeDelivery) DevicesChanged() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, notifications := range d.subscribers {
		select {
		case notifications <- struct{}{}:
		default:
		}
	}
}

func (d *pipeDelivery) Shutdown() error {
	d.shutdownOnce.Do(func() {
		d.mu.Lock()
		d.stopping = true
		listener := d.listener
		connections := make([]net.Conn, 0, len(d.connections))
		for conn := range d.connections {
			connections = append(connections, conn)
		}
		d.mu.Unlock()

		if listener != nil {
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				d.shutdownErr = err
			}
		}
		for _, conn := range connections {
			_ = conn.Close()
		}

		d.wg.Wait()
	})

	return d.shutdownErr
}
