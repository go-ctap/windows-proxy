package main

import (
	"log/slog"
	"os"

	ghid "github.com/go-ctap/hid"
	"github.com/go-ctap/windows-proxy/internal/config"
	"github.com/go-ctap/windows-proxy/internal/domain"
	"github.com/go-ctap/windows-proxy/pkg/devnotify"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

const svcName = "CtapProxy"

type program struct {
	logger   *slog.Logger
	config   *config.Config
	delivery domain.Delivery
}

func (p *program) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	var wlog *eventlog.Log
	wlog, err := eventlog.Open(svcName)
	if err != nil {
		p.logger.Error("Error while opening event log!", "err", err)
		return
	}
	defer func() {
		_ = wlog.Close()
	}()

	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	go func() {
		l, err := p.delivery.Listen()
		if err != nil {
			p.logger.Error("Error while getting a listener!", "err", err)
			os.Exit(1)
		}

		if err := p.delivery.Serve(l); err != nil {
			p.logger.Error("Error while serving delivery!", "err", err)
			os.Exit(1)
		}
	}()

	_ = wlog.Info(1, "Service started!")

	if statusHandle := svc.StatusHandle(); statusHandle != 0 {
		_ = wlog.Info(1, "Registering device notification")
		notification, err := devnotify.RegisterDeviceNotification(statusHandle)
		if err != nil {
			_ = wlog.Error(2, "Failed to register device notification")
			os.Exit(1)
		}
		defer func() {
			if err := devnotify.UnregisterDeviceNotification(notification); err != nil {
				p.logger.Error("Failed to unregister device notification", "err", err)
			}
		}()
	} else {
		receiver, err := ghid.Events()
		if err != nil {
			p.logger.Error("Failed to monitor HID device events", "err", err)
		} else {
			done := make(chan struct{})
			go func() {
				defer close(done)
				for event := range receiver.Listen() {
					if isFIDOEvent(event) {
						p.delivery.DevicesChanged()
					}
				}
			}()
			defer func() {
				_ = receiver.Close()
				<-done
			}()
		}
	}

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.DeviceEvent:
			p.delivery.DevicesChanged()
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			if err := p.delivery.Shutdown(); err != nil {
				p.logger.Error("Error while shitting down main delivery!", "err", err)
			}
			break loop
		default:
			p.logger.Info("Service shut down!", "cmd", c.Cmd)
		}
	}

	changes <- svc.Status{State: svc.StopPending}
	return
}

func isFIDOEvent(event ghid.DeviceEvent) bool {
	if event.Type != ghid.DeviceEventConnected && event.Type != ghid.DeviceEventDisconnected {
		return false
	}
	if event.Err != nil || event.DeviceInfo == nil {
		return true
	}
	return (event.DeviceInfo.UsagePage == 0 || event.DeviceInfo.UsagePage == 0xf1d0) &&
		(event.DeviceInfo.Usage == 0 || event.DeviceInfo.Usage == 0x01)
}

func (p *program) run(svcName string, isDebug bool) {
	if !isDebug {
		if err := eventlog.InstallAsEventCreate(svcName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
			p.logger.Error("Error while installing event log!", "err", err)
		}
	}

	p.logger.Info("Starting service!", "svc_name", svcName)
	run := svc.Run
	if isDebug {
		run = debug.Run
	}
	if err := run(svcName, p); err != nil {
		p.logger.Error("Error while running service!", "err", err, "svc_name", svcName)
		return
	}
	p.logger.Info("Service successfully shut down!", "svc_name", svcName)
}
