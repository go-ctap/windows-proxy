# Changelog

## v0.2.0 - 2026-07-18

### Changed

- Migrated the HID proxy protocol dependency from `github.com/go-ctap/ctaphid/pkg/hidproxy` to `github.com/go-ctap/ctap/hidproxy v0.30.1`.
- Updated `github.com/go-ctap/hid` to `v0.9.1` and adapted proxy I/O to its context-aware `Read` and `Write` API.
- Added a control-message deadline and graceful shutdown for accepted connections.
- Unregister device notifications when the service stops.
- Broadcast `CommandDevicesChanged` to subscribed clients when the HID topology changes.

## v0.1.0 - 2026-05-04

### Changed

- Updated release dependencies to `github.com/go-ctap/ctaphid v0.8.5`, `github.com/go-ctap/hid v0.4.2`, `github.com/fxamacker/cbor/v2 v2.9.2`, and current `golang.org/x` modules.
- Handle accepted transport connections concurrently, so enumeration and proxy sessions no longer block the listener loop.
- Harden proxy session lifecycle with per-device active-session tracking, structured session close reasons, and compact CTAPHID packet debug logging.
- Forward complete 65-byte HID reports from pipe to HID and keep full writes from HID back to the pipe.
- Represent device notification handles as integer handles to keep `go vet` clean.

### Verified

- `go generate ./...`
- `go test ./...`
- `go vet ./...`
- `go build -o _obj\ctap-proxy-service.exe .\cmd\service`
