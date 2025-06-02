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
