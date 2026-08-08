package config

import "github.com/telesma-app/windows-proxy/internal/infra/transport"

type Config struct {
	Transport *transport.Config
}
