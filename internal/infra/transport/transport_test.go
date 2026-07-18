package transport

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/go-ctap/windows-proxy/pkg/proxy"
	proxyprotocol "github.com/go-ctap/windows-proxy/protocol"
)

func newTestDelivery() *pipeDelivery {
	return NewDelivery(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&Config{Debug: true},
		proxy.New(),
	).(*pipeDelivery)
}

func TestDevicesChangedSubscription(t *testing.T) {
	d := newTestDelivery()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = d.Serve(l) }()
	defer func() { _ = d.Shutdown() }()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	subscribe, err := proxyprotocol.NewMessage(proxyprotocol.CommandDevicesChanged, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscribe.WriteTo(conn); err != nil {
		t.Fatal(err)
	}

	initial, err := proxyprotocol.ParseMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Command != proxyprotocol.CommandDevicesChanged {
		t.Fatalf("initial command = %d", initial.Command)
	}

	d.DevicesChanged()
	event, err := proxyprotocol.ParseMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if event.Command != proxyprotocol.CommandDevicesChanged {
		t.Fatalf("event command = %d", event.Command)
	}
}

func TestDevicesChangedCoalescesForSlowSubscriber(t *testing.T) {
	d := newTestDelivery()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	notifications := make(chan struct{}, 1)
	notifications <- struct{}{}
	d.subscribers[server] = notifications

	done := make(chan struct{})
	go func() {
		d.DevicesChanged()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("DevicesChanged blocked on a slow subscriber")
	}
	if got := len(notifications); got != 1 {
		t.Fatalf("pending notifications = %d, want 1", got)
	}
}

func TestShutdownClosesActiveConnectionsAndIsIdempotent(t *testing.T) {
	d := newTestDelivery()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- d.Serve(l) }()

	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for {
		d.mu.Lock()
		active := len(d.connections)
		d.mu.Unlock()
		if active == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection was not accepted")
		}
		time.Sleep(time.Millisecond)
	}

	if err := d.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := d.Shutdown(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
}

func TestShutdownBeforeServe(t *testing.T) {
	d := newTestDelivery()
	if err := d.Shutdown(); err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Serve(l); err != nil {
		t.Fatal(err)
	}
}
