# LanPane

Share text, code, images, and files between devices on your local network. Zero config.

## Quick Start

```bash
# On every machine:
git clone <your-repo-url> lanpane
cd lanpane
go run .
```

Open `http://localhost:7753` in your browser. That's it.

## How It Works

1. **First device** starts → becomes the **hub**, shows a 6-character access code
2. **Other devices** start → auto-discover the hub via UDP broadcast, connect as **spokes**
3. If direct spoke -> hub connection is blocked, spoke asks hub to dial back (reverse connect)
4. If auth is required, enter the access code on each spoke (one-time, saved for reconnects)
5. All panes sync in real-time across all devices

## Features

- **Mixed-content panes** — each pane holds text, code, markdown, images, and files
- **Paste images** — `Ctrl+V` / `Cmd+V` an image from clipboard → instantly shared
- **Copy images** — click 📋 on any image to copy it to clipboard on the other device
- **Drag & drop files** — drop files into the UI
- **Syntax highlighting** — toggle any text block to "code" mode, pick a language
- **Markdown rendering** — toggle to "md" mode for rendered markdown
- **Multi-device** — not limited to two devices
- **Auto-discovery** — devices find each other via UDP broadcast, no IP address needed
- **Configurable auth** — run with optional auth for frictionless LAN use, or require a token
- **Dual-direction connect fallback** — handles one-way firewall rules better by trying reverse hub -> spoke dialing
- **HTTPS UI/API** — optional built-in TLS listener for browser access
- **Offline persistence** — panes persist locally, survive restarts

## Architecture

```
Hub-Spoke with auto-election:
  ┌─────┐  UDP broadcast   ┌──────┐
  │Spoke│◄─────────────────►│ Hub  │
  │ :77xx│  WebSocket+HTTP   │:7753 │
  └─────┘                   └──────┘
                               ▲
  ┌─────┐  WebSocket+HTTP      │
  │Spoke│◄─────────────────────┘
  │ :77xx│
  └─────┘
```

- **Hub** runs the WebSocket server and relays changes
- **Spokes** connect out to the hub (works through firewalls)
- If no hub exists, the first device self-promotes
- All communication is outbound from spokes → works even when incoming ports are blocked

## Options

```
go run . -port 8080   # Use a different port (default: 7753)
go run . -auth optional
go run . -auth required
go run . -https=true -https-port 8443
```

Default auth mode is `optional` (stored in `~/.lanpane/config.json` as `authMode`).

By default, HTTPS is enabled on `port+1` using a generated self-signed certificate in `~/.lanpane/certs/`.

## Data Storage

All data is stored in `~/.lanpane/`:
- `config.json` — device ID, name, token settings (`savedToken`, `authMode`)
- `panes.json` — all pane content
- `files/` — uploaded files and images

## Requirements

- Go 1.21+
- A LAN where UDP broadcast works (most home/office networks)
