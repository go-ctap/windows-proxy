# telesma-app/windows-proxy

`telesma-app/windows-proxy` is a Windows service that gives regular user applications access to FIDO2 HID devices.
The service opens the devices with its own permissions and forwards CTAPHID data through a Windows named pipe.

The [`telesma-app/ctap`](https://github.com/telesma-app/ctap) library can use this service, so an application does not need
to run as administrator.

## Features

- Runs as the `CtapProxy` Windows service.
- Listens on `\\.\pipe\ctaphid`.
- Lists connected FIDO2 HID devices.
- Proxies CTAPHID packets in both directions.
- Notifies clients when the device list may have changed.
- Shares one physical HID connection across concurrent proxy sessions for each
  device. CTAPHID channel IDs keep the clients isolated.

## Requirements

- Windows
- Go 1.26.3 or newer
- Administrator rights to install and manage the service

## Build and test

Run these commands in PowerShell:

```powershell
go build -o ctap-proxy-service.exe ./cmd/service
go test ./...
```

## Install the service

Open PowerShell as administrator. Copy the executable to a permanent location, then create and start the service:

```powershell
New-Item -ItemType Directory -Force C:\CtapProxy
Copy-Item .\ctap-proxy-service.exe C:\CtapProxy\
sc.exe create CtapProxy binPath= C:\CtapProxy\ctap-proxy-service.exe start= auto
sc.exe start CtapProxy
```

To stop and remove the service:

```powershell
sc.exe stop CtapProxy
sc.exe delete CtapProxy
```

The executable supports two development flags:

- `-svcDebug` enables debug logs.
- `-transportDebug` uses TCP at `localhost:44080` instead of the named pipe.

Do not use the TCP transport in production.

## Protocol

Go clients should use the public
[`protocol`](https://pkg.go.dev/github.com/telesma-app/windows-proxy/protocol) package. It contains the pipe path,
command values, and message helpers.

Each connection starts with one control message:

| Field | Size | Description |
|:------|:-----|:------------|
| Command | 1 byte | Command number |
| Length | 2 bytes | Data size as a big-endian `uint16` |
| Data | `Length` bytes | Command data, encoded as CBOR when needed |

The service supports these commands:

| Command | Value | Request | Result |
|:--------|:------|:--------|:-------|
| `CommandEnumerate` | `0x01` | Empty data | Returns a CBOR list of FIDO2 HID devices, then closes the connection |
| `CommandStart` | `0x02` | CBOR device path from `CommandEnumerate` | Starts a raw CTAPHID stream on the same connection |
| `CommandDevicesChanged` | `0x03` | Empty data | Keeps the connection open and sends empty notification messages |

### Raw CTAPHID stream

After `CommandStart`, the client sends complete 65-byte HID reports: one report ID byte and one 64-byte CTAPHID
packet. The service writes data returned by the HID device back to the same connection.

The requested path must belong to a currently connected FIDO2 device. If the path is invalid or the device cannot be
opened, the service closes the connection. Concurrent sessions for one path share the same physical HID connection;
complete client messages are serialized when written, and incoming reports are fanned out so each client can filter
its own CTAPHID channel.

### Device notifications

A `CommandDevicesChanged` subscription needs its own connection. The service sends one notification immediately and
another when a FIDO2 device may have been connected or removed.

A notification does not contain device details. After receiving it, the client should use `CommandEnumerate` on a
new connection and update its device list.

## License

Licensed under the [Apache License 2.0](LICENSE).
