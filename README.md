# LanPane

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Share text, code, images, and files between devices on your local network. Zero config.

## Quick Start

```bash
git clone https://github.com/mmdemirbas/lanpane.git
cd lanpane
make            # macOS / Linux
make.cmd        # Windows
```

Open `https://localhost:7753` in your browser (accept the self-signed certificate warning once). That's it.

Only Go is required. All frontend assets are vendored in the repo — no npm, no internet needed at build time.

## Commands

Both `make` (macOS/Linux) and `make.cmd` (Windows) support the same subcommands:

| Command | Description |
|---|---|
| `make` | Run the development server (default) |
| `make build` | Build binary for current platform → `bin/` |
| `make build-all` | Cross-compile for macOS, Windows, Linux → `bin/` |
| `make test` | Run tests |
| `make vendor` | Re-download vendored JS/CSS (only to update lib versions) |
| `make clean` | Remove build artifacts |

Or use plain Go directly:

```bash
go run ./cmd/lanpane                         # Run
go build -o bin/lanpane ./cmd/lanpane        # Build
go test ./cmd/lanpane                        # Test
```

## Project Structure

```
cmd/lanpane/        Go source (package main)
  web/              Frontend (HTML/JS/CSS, embedded into binary)
    vendor/         Vendored JS/CSS/fonts (checked into git)
Makefile            Build commands (macOS / Linux)
make.cmd            Build commands (Windows)
bin/                Build output (gitignored)
```

## How It Works

1. **First device** starts → becomes the **hub**
2. **Other devices** start → auto-discover the hub via UDP broadcast, connect as **spokes**
3. If direct spoke → hub connection is blocked, spoke asks hub to dial back (reverse connect)
4. All panes sync in real-time across all devices

## Features

- **Mixed-content panes** — each pane holds text, code, markdown, images, and files
- **Paste images** — `Ctrl+V` / `Cmd+V` an image from clipboard → instantly shared
- **Drag & drop files** — drop files into the UI, or use the file chooser on mobile
- **Syntax highlighting** — live highlighting in the editor with language auto-detection
- **Markdown preview** — rendered markdown with per-code-block copy buttons
- **Word wrap toggle** — `Alt+W` to toggle wrap in editor and preview
- **Tab management** — drag-and-drop reordering, per-tab preview state, inline delete confirmation
- **Collapsible sidebar** — toggle to save screen space, state persisted
- **Auto-naming** — panes auto-title from content (respects manual edits)
- **Multi-device** — not limited to two devices
- **Auto-discovery** — devices find each other via UDP broadcast, no IP address needed
- **Dual-direction connect** — handles one-way firewall rules via reverse hub → spoke dialing
- **HTTPS everywhere** — all traffic (UI, API, inter-node WebSocket) uses TLS with auto-generated self-signed certificates
- **Offline persistence** — panes persist locally, survive restarts
- **Mobile friendly** — responsive layout, touch targets, file chooser

## Architecture

```
Hub-Spoke with auto-election:
  ┌─────┐  UDP broadcast    ┌──────┐
  │Spoke│◄─────────────────►│ Hub  │
  │:77xx│  WSS (WebSocket)  │:7753 │
  └─────┘                   └──────┘
                                ▲
  ┌─────┐  WSS (WebSocket)      │
  │Spoke│◄──────────────────────┘
  │:77xx│
  └─────┘
```

- **Hub** runs the HTTPS/WebSocket server and relays changes
- **Spokes** connect out to the hub (works through firewalls)
- If no hub exists, the first device self-promotes
- All communication is outbound from spokes → works even when incoming ports are blocked

## Keyboard Shortcuts

| Shortcut | Action |
|---|---|
| `Alt + W` | Toggle word wrap |
| `Escape` | Exit preview mode |

## Options

```
./bin/lanpane -port 8080    # Use a different port (default: 7753)
```

All traffic uses HTTPS with an auto-generated self-signed certificate stored in `~/.lanpane/certs/`. On first access, your browser will show a certificate warning — accept it once to proceed.

## Data Storage

All data is stored in `~/.lanpane/`:
- `config.json` — device ID and name
- `panes.json` — all pane content
- `files/` — uploaded files and images
- `certs/` — auto-generated TLS certificate and key

## Security Notes

LanPane is designed for **trusted local networks**.

- **No authentication.** Any device on the LAN can connect and read/write panes. This is by design for zero-friction LAN sharing.
- **WebSocket origin checks are disabled** to allow access from any local browser.
- **CORS is permissive** (`Access-Control-Allow-Origin: *`) on the SSE endpoint for the same reason.
- **Self-signed HTTPS** is used for all traffic to avoid browser mixed-content warnings, but does not provide CA-trusted encryption.
- **Markdown content is sanitized** with DOMPurify before rendering to prevent XSS.

This tool is **not intended for use over the public internet**.

## Requirements

- Go 1.22+
- A LAN where UDP broadcast works (most home/office networks)

## License

[MIT](LICENSE)
