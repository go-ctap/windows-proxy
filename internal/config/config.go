package config

import "github.com/go-ctap/windows-proxy/internal/infra/transport"

type Config struct {
	Transport *transport.Config
}
