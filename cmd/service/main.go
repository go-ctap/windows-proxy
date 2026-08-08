package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/telesma-app/windows-proxy/internal/config"
	"github.com/telesma-app/windows-proxy/internal/infra/transport"
	"github.com/telesma-app/windows-proxy/pkg/proxy"

	"golang.org/x/sys/windows/svc"
)

var (
	svcDebugMode       bool
	transportDebugMode bool
)

func main() {
	flag.BoolVar(&svcDebugMode, "svcDebug", false, "Service debug mode")
	flag.BoolVar(&transportDebugMode, "transportDebug", false, "Transport debug mode")
	flag.Parse()

	conf := &config.Config{
		Transport: &transport.Config{
			Address:                 "localhost:44080",
			Debug:                   transportDebugMode,
			AllowAuthenticatedUsers: true,
		},
	}

	level := slog.LevelInfo
	if svcDebugMode {
		level = slog.LevelDebug
	}

	lvl := new(slog.LevelVar)
	lvl.Set(level)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	}))

	prx := proxy.New(proxy.WithLogger(logger))
	delivery := transport.NewDelivery(logger, conf.Transport, prx)

	prog := &program{
		logger:   logger,
		config:   conf,
		delivery: delivery,
	}

	inService, err := svc.IsWindowsService()
	if err != nil {
		logger.Error("Failed to determine if we are running in service", "err", err)
		os.Exit(1)
	}

	prog.run(svcName, !inService)
}
