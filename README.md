# LanPane

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Share text, code, images, and files between devices on your local network. Zero config.

## Quick Start

```bash
git clone https://github.com/mmdemirbas/lanpane.git
cd lanpane
go run .
```

Open `https://localhost:7753` in your browser. That's it.

## How It Works

1. **First device** starts → becomes the **hub**, shows a 6-character access code
2. **Other devices** start → auto-discover the hub via UDP broadcast, connect as **spokes**
3. If direct spoke → hub connection is blocked, spoke asks hub to dial back (reverse connect)
4. If auth is required, enter the access code on each spoke (one-time, saved for reconnects)
5. All panes sync in real-time across all devices

## Features

- **Mixed-content panes** — each pane holds text, code, markdown, images, and files
- **Paste images** — `Ctrl+V` / `Cmd+V` an image from clipboard → instantly shared
- **Drag & drop files** — drop files into the UI
- **Syntax highlighting** — live highlighting in the editor with language auto-detection
- **Markdown preview** — rendered markdown with per-code-block copy buttons
- **Word wrap toggle** — `Ctrl+W` / `Cmd+W` to toggle wrap in editor and preview
- **Tab management** — drag-and-drop reordering, per-tab preview state, inline delete
- **Collapsible sidebar** — `Ctrl+B` / `Cmd+B` to toggle, state persisted
- **Auto-naming** — panes auto-title from content (respects manual edits)
- **Multi-device** — not limited to two devices
- **Auto-discovery** — devices find each other via UDP broadcast, no IP address needed
- **Configurable auth** — run with optional auth for frictionless LAN use, or require a token
- **Dual-direction connect** — handles one-way firewall rules via reverse hub → spoke dialing
- **HTTPS everywhere** — all traffic (UI, API, inter-node) uses TLS with auto-generated self-signed certificates
- **Offline persistence** — panes persist locally, survive restarts

## Architecture

```
Hub-Spoke with auto-election:
  ┌─────┐  UDP broadcast    ┌──────┐
  │Spoke│◄─────────────────►│ Hub  │
  │:77xx│  WebSocket+HTTP   │:7753 │
  └─────┘                   └──────┘
                                ▲
  ┌─────┐  WebSocket+HTTP       │
  │Spoke│◄──────────────────────┘
  │:77xx│
  └─────┘
```

- **Hub** runs the WebSocket server and relays changes
- **Spokes** connect out to the hub (works through firewalls)
- If no hub exists, the first device self-promotes
- All communication is outbound from spokes → works even when incoming ports are blocked

## Keyboard Shortcuts

| Shortcut | Action |
|---|---|
| `Alt + W` | Toggle word wrap |

## Options

```
go run . -port 8080         # Use a different port (default: 7753)
go run . -auth optional     # No access code needed (default)
go run . -auth required     # Require access code on spokes
```

Default auth mode is `optional` (stored in `~/.lanpane/config.json` as `authMode`).

All traffic uses HTTPS with an auto-generated self-signed certificate stored in `~/.lanpane/certs/`. On first access, your browser will show a certificate warning — accept it once to proceed.

## Data Storage

All data is stored in `~/.lanpane/`:
- `config.json` — device ID, name, token settings
- `panes.json` — all pane content
- `files/` — uploaded files and images
- `certs/` — auto-generated TLS certificate and key

## Security Notes

LanPane is designed for **trusted local networks**. Key security properties:

- **Auth is optional by default.** In `optional` mode, any device on the LAN can connect without a code. Use `-auth required` for access control.
- **HTTP API endpoints are unauthenticated.** Auth is enforced at the WebSocket layer. Any device that can reach the HTTP port can read/write panes via the REST API. This is by design for LAN simplicity.
- **WebSocket origin checks are disabled** to allow access from any local browser.
- **CORS is permissive** (`Access-Control-Allow-Origin: *`) on the SSE endpoint for the same reason.
- **Self-signed HTTPS** is available to avoid browser mixed-content warnings, but does not provide CA-trusted encryption.
- **Markdown content is sanitized** with DOMPurify before rendering to prevent XSS.

This tool is **not intended for use over the public internet**.

## Requirements

- Go 1.22+
- A LAN where UDP broadcast works (most home/office networks)

## License

[MIT](LICENSE)
