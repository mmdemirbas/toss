# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make              # Run dev server at https://localhost:7753 (watches for changes)
make build        # Build binary for current platform → bin/
make build-all    # Cross-compile: macOS arm64/amd64, Windows, Linux
make test         # Run Go tests
make vendor       # Re-download vendored JS/CSS libs (highlight.js, marked.js, DOMPurify)
make clean        # Remove build artifacts
```

Run a single test:
```bash
go test ./cmd/toss -run TestName
```

## Architecture

**Toss** is a zero-config LAN file/content sharing tool. A single Go binary serves a web UI and syncs panes (text/code/images/files) across devices in real time.

### Hub-Spoke Topology

Devices auto-discover each other via UDP broadcast on port 7754. The first device becomes the hub; the rest connect as spokes. If the hub disappears, the next device self-promotes. If a spoke can't dial the hub (one-way firewall), the hub dials back.

```
Browser ──SSE/HTTPS──► Hub :7753 ◄──WSS──► Spoke :77xx ◄──── Browser
                         │
                         └──WSS──► Spoke 2 :77xx ◄──── Browser
```

### Core Files (`cmd/toss/`)

| File | Responsibility |
|---|---|
| `main.go` | Entry point: flags, store init, TLS server, SSE endpoint |
| `node.go` | Hub/spoke state machine, WebSocket relay, SSE dispatch |
| `handlers.go` | HTTP API: panes CRUD, file upload/download, `/api/status`, `/ws` upgrade |
| `store.go` | Thread-safe persistence to `~/.toss/` (panes.json, config.json, files/) |
| `discovery.go` | UDP broadcast: hub election, spoke discovery, collision arbitration |
| `models.go` | `Pane`, `Device`, `WSMessage`, and all payload types |
| `clipboard.go` | Polls clipboard every 500ms, echo-prevents via hash, syncs across devices |
| `clipboard_image.go` | Platform-specific image capture (AppleScript/xclip/Win32), Base64 transport |
| `clipboard_files.go` | File list parsing, store/forward files, recreate on receive |
| `tls.go` | Auto-generates self-signed cert with all local IPs as SANs |

Frontend lives in `web/` and is embedded into the binary at build time.

### Data Flow

**Pane edit:** Browser → `PUT /api/panes/{id}` → store update → hub broadcasts `pane_update` over WSS to all spokes → spokes push to their browsers via SSE.

**Clipboard sync:** `ClipboardMonitor` detects change → hash check (echo prevention) → `ClipboardPayload` → hub relays to all spokes → receiving side writes to local clipboard and optionally creates a pane.

**File upload:** `POST /api/files` (multipart) → stored in `~/.toss/files/{id}` → pane created with download link → synced like any other pane.

### Persistence

All runtime data lives in `~/.toss/`:
- `config.json` — device ID, name, clipboard settings
- `panes.json` — all pane content
- `files/` — uploaded/clipboard files
- `certs/` — self-signed TLS cert (auto-generated, valid 2 years)
- `clipboard_received/` — files received from remote clipboard

### Dependencies

Only two external Go packages:
- `gorilla/websocket` — WebSocket connections between nodes
- `atotto/clipboard` — system clipboard read/write

Vendored JS (in `web/vendor/`): highlight.js 11.9, marked.js 12.0.1, DOMPurify 3.0.9.

### Security Posture

Designed for trusted LANs only — no authentication, permissive CORS. Markdown is sanitized with DOMPurify. Path traversal is blocked at the Go HTTP mux level. File ID input is validated (no empty IDs). Not intended for internet-facing deployment.
