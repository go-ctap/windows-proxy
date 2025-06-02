# go-ctaphid-windows-proxy

---

## Overview

On Windows, direct communication with FIDO2 tokens using the CTAP protocol (e.g., over HID) typically requires
administrator privileges. This can be a significant hurdle for applications that need to interact with
FIDO2 authenticators without requiring the user to run the entire application with elevated rights.

This program addresses this limitation by acting as a **proxy service**. It is designed to:

1. **Run as a Windows Service:** This allows the proxy to run with the necessary privileges to access HID devices
   directly.
2. **Listen on a Named Pipe:** It exposes a named pipe endpoint for inter-process communication.
3. **Proxy CTAP Requests:** Client applications can send CTAP requests to this named pipe. The service then forwards
   these requests to the actual FIDO2 HID device and returns the responses.

This architecture enables unprivileged applications to communicate with FIDO2 tokens. Specifically,
my [go-ctaphid](https://github.com/savely-krasovsky/go-ctaphid) library is designed to leverage this proxy,
allowing Go applications to interact with FIDO2 tokens on Windows without needing administrator rights for
the application itself.

In essence, this program provides a bridge for unprivileged CTAP access to FIDO2 tokens on Windows.

Okay, here's a documentation draft for your proxy protocol based on the provided Go code.

---

## HID Proxy Protocol Documentation

This document describes the communication protocol used between a client application and the HID Proxy service
over a Windows Named Pipe.

### 1. Overview

The protocol enables unprivileged applications to communicate with FIDO2 HID devices. It involves two main phases:

1. **Control Phase:** The client sends structured messages to enumerate available FIDO2 devices and to select one
   for proxying.
2. **Proxy Phase:** Once a device is selected, the named pipe connection switches to a raw byte stream,
   directly proxying CTAPHID packets between the client and the selected HID device.

### 2. Transport

*   **Named Pipe Path:** `\\.\pipe\ctaphid`
   *   Clients connect to this named pipe to communicate with the proxy service.

### 3. Message Structure (Control Phase)

All messages exchanged during the Control Phase follow this structure:

| Field   | Size (bytes) | Description                                                      |
|:--------|:-------------|:-----------------------------------------------------------------|
| Command | 1            | The command ID (see [Commands](#4-commands-control-phase)).      |
| Length  | 2            | Big-endian `uint16` representing the length of the `Data` field. |
| Data    | `Length`     | Payload specific to the command. Often CBOR-encoded.             |

**Data Encoding:** Unless otherwise specified, the `Data` payload for requests and responses that contain complex
structures (like device information or paths) is CBOR-encoded.

### 4. Commands (Control Phase)

#### 4.1. `CommandEnumerate` (`0x01`)

- **Purpose:** Requests a list of available FIDO2 HID devices.
- **Client Request:**
  - Command: `0x01`
  - Length: `0x0000` (0)
  - Data: (empty)
  
- **Server Response (Success):**
  - Command: `0x01`
  - Length: Size of the CBOR-encoded device list.
  - Data: A CBOR-encoded array of `DeviceInfo` objects. Each `DeviceInfo` object contains details about a discovered
    FIDO2 HID device, typically including fields like:
    - `Path`: A system-specific path to the device (e.g., `\\?\hid#vid_xxxx&pid_yyyy...`). **This path is crucial for
      the `CommandStart` request.**
    - `VendorID`: USB Vendor ID.
    - `ProductID`: USB Product ID.
    - `ManufacturerString`: Manufacturer name.
    - `ProductString`: Product name.
    - (Other fields as provided by the underlying `go-hid` library for FIDO2 devices, specifically those with
      `UsagePage == 0xf1d0` and `Usage == 0x01`).
- **Server Response (Error):**
  - If an error occurs during enumeration (e.g., HID subsystem error), the server might close the connection or send an
    error response (the current code implies connection closure or no specific error message format for this command).

#### 4.2. `CommandStart` (Value: `0x02`)

- **Purpose:** Instructs the proxy to start relaying raw CTAPHID packets for a specific FIDO2 HID device.
  After a successful `CommandStart`, the protocol transitions to the [Proxy Phase](#5-proxy-phase-after-successful-commandstart).
- **Client Request:**
  - Command: `0x02`
  - Length: Size of the CBOR-encoded device path string.
  - Data: A CBOR-encoded string containing the `Path` of the FIDO2 HID device to connect to. This path should be one of
    the paths obtained from a previous `CommandEnumerate` response.
- **Server Response / Behavior:**
  - **Success:**
    - The server attempts to open the specified HID device.
    - If successful, the server **does not send a structured `Message` response**.
    - Instead, the named pipe connection transitions into the [Proxy Phase](#5-proxy-phase-after-successful-commandstart).
      The pipe remains open, and subsequent data sent by the client will be treated as raw CTAPHID packets destined
      for the HID device.
  - **Failure:**
    - If the server fails to open the specified HID device (e.g., device not found, access denied at the HID level),
      the proxy service will typically close the named pipe connection. The client will detect this as an EOF or
      connection error.

### 5. Proxy Phase (After successful `CommandStart`)

Once `CommandStart` is successfully processed:

- **Client to Server (to HID Device):**
  - The client writes raw CTAPHID request packets directly to the named pipe. These packets should be formatted as
    expected by the FIDO2 HID device (typically 64-byte reports, potentially prefixed with a HID report ID byte
    like `0x00` if the underlying HID library or device requires it. The `go-ctaphid` library handles this formatting).
  - The proxy service reads these bytes and writes them directly to the selected HID device. The server reads
    up to 65 bytes at a time from the pipe for forwarding.
- **Server (from HID Device) to Client:**
  - The proxy service reads raw CTAPHID response packets from the HID device.
  - These packets are written directly to the named pipe for the client to read.

**Data Flow in Proxy Phase:**

```
Client App  <--Raw CTAPHID Packets--> Named Pipe <--> Proxy Service <--> HID Device
```

**Termination:**
The proxy session for a given device ends when:
*   The client closes its end of the named pipe.
*   An error occurs during communication with the HID device, causing the proxy to close the connection.
*   The HID device is disconnected.

In these cases, the named pipe connection will be closed. To communicate with another device or retry,
the client must establish a new connection and restart the protocol from the Control Phase.
