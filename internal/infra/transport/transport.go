package transport

import (
	"errors"
	"log/slog"
	"net"
	"strings"

	"github.com/go-ctap/windows-proxy/internal/domain"
	"github.com/go-ctap/windows-proxy/pkg/proxy"

	"github.com/Microsoft/go-winio"
	"github.com/fxamacker/cbor/v2"
	"github.com/go-ctap/ctaphid/pkg/hidproxy"
)

type pipeDelivery struct {
	logger *slog.Logger
	config *Config
	proxy  *proxy.Proxy
	done   chan struct{}
}

func NewDelivery(logger *slog.Logger, config *Config, p *proxy.Proxy) domain.Delivery {
	d := &pipeDelivery{
		logger: logger,
		config: config,
		proxy:  p,
		done:   make(chan struct{}),
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
	addr := hidproxy.NamedPipePath
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
	go func() {
		// Wait done to close listener
		<-d.done
		_ = l.Close()
	}()

	defer func() {
		// Notify that listener closed
		d.done <- struct{}{}
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				d.logger.Debug("Pipe listener closed")
				return nil
			}
			d.logger.Error("Pipe accept error", "err", err)
			continue
		}
		d.logger.Debug("Accepted pipe connection")

		go d.handleConn(conn)
	}
}

func (d *pipeDelivery) handleConn(conn net.Conn) {
	msg, err := hidproxy.ParseMessage(conn)
	if err != nil {
		d.logger.Error("Parse message error", "err", err)
		_ = conn.Close()
		return
	}

	switch msg.Command {
	case hidproxy.CommandEnumerate:
		devInfos, err := d.proxy.Enumerate()
		if err != nil {
			d.logger.Error("Device enumeration error", "err", err)
			_ = conn.Close()
			return
		}

		msg, err := hidproxy.NewMessage(hidproxy.CommandEnumerate, devInfos)
		if err != nil {
			d.logger.Error("NewMessage error", "err", err)
			_ = conn.Close()
			return
		}

		if _, err := msg.WriteTo(conn); err != nil {
			d.logger.Error("WriteTo error", "err", err)
			_ = conn.Close()
			return
		}

		d.logger.Info("Enumerate response sent")
		_ = conn.Close()
	case hidproxy.CommandStart:
		var path string
		if err := cbor.Unmarshal(msg.Data, &path); err != nil {
			d.logger.Error("Unmarshal error", "err", err)
			_ = conn.Close()
			return
		}

		d.logger.Debug("Start command received", "path", path)
		d.proxy.Proxy(conn, path)
	default:
		d.logger.Error("Unknown command", "command", msg.Command)
		_ = conn.Close()
	}
}

func (d *pipeDelivery) Shutdown() error {
	// Close listener
	d.done <- struct{}{}
	// Wait for listener close
	<-d.done
	return nil
}
