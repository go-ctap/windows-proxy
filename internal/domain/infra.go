package domain

import (
	"net"
)

type Delivery interface {
	Listen() (net.Listener, error)
	Serve(l net.Listener) error
	DevicesChanged()
	Shutdown() error
}
