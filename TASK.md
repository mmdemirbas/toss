# Autonomous Improvement Task

## Status
Active — continue where left off.

## Directive
Improve all autonomously. Focus one thing at a time. Make sure it is working and high quality before committing. Commit each logical change unit separately.

## Coverage Progress
- Baseline: 20.6%
- Current: 52.0% (as of last session, branch: main, last commit: 3bedffa)
- All commits pass `go test ./cmd/toss/... -race` and `go vet ./...`

---

## Next Work (priority order)

### 1. Hub registration of new spoke triggers device broadcast
- When a spoke connects and is authenticated, hub broadcasts `devices` to all clients.
- The `n.broadcastDevices()` call in `acceptSpokeConn` is not covered.
- Test: connect a second spoke and verify both receive the updated device list.

### 2. `disconnectClient` cleanup path
- When a spoke disconnects, hub removes it from clients and devices maps, then broadcasts.
- `cmd/toss/node.go` function `disconnectClient`
- Test: connect a spoke, wait for it to register, then disconnect it and verify hub's device list shrinks.

### 3. Hub `pane_delete` message with missing paneId
- `hubHandleMessage` routes `pane_delete` with empty PaneID.
- Should be a no-op (pane not in store, nothing to delete).
- Test: send a `pane_delete` with empty PaneID from spoke to hub, verify no panic.

### 4. `receiveClipboardFiles` happy path
- When `ensureFileLocal` returns true, file is copied to `clipboard_received` dir.
- Test: write a file into the store, call `receiveClipboardFiles` with its ref, verify the file appears in `clipboard_received`.

### 5. `hubHandleMessage` unknown message type
- `hubHandleMessage` with an unrecognized `msg.Type` should log and return.
- Test: send an unrecognized type via spoke, verify hub doesn't panic.

### 6. Integration tests for hub-spoke sync conflicts (M1)
- Pane version conflict: two spokes edit same pane with different versions; lower version must be dropped (see `store.go:UpsertPane`).
- Concurrent delete + update: spoke A deletes pane X, spoke B updates pane X — verify hub handles without panic.
- Spoke reconnection with partial sync: spoke disconnects, hub accumulates pane updates, spoke reconnects — verify spoke converges.

---

## Bug Fixes / Correctness

### B1. Race window in `Store.UpsertPane` (M4)
- `store.go:UpsertPane` unlocks before calling `savePanes()`. A crash between the unlock and the save leaves the in-memory map out of sync with disk.
- Current design is intentional for performance (eventual consistency). If changing: hold the lock during save, or document the trade-off explicitly.
- Action: add a comment in `UpsertPane` documenting the eventual-consistency design choice and the crash window.

### B2. Clipboard echo prevention edge case (M5)
- `clipboard.go:check`: if a user copies text that happens to match `lastWrittenText` from a peer, the local change is silently ignored.
- Current risk is low (LAN-only, low-collision probability).
- Action: document the edge case in a comment near the echo-prevention logic.

---

## Configuration

### C1. Hardcoded size limits duplicated across files (M2)
- `handlers.go` and `clipboard_files.go` both hardcode `50<<20` for max file size independently.
- If one changes, the other must be updated manually.
- Action: consolidate into a single named constant (e.g., `maxFileSize`) shared by both files. No need for env-var overrides given the LAN-only scope.

---

## Features (Backlog)

### F1. Pane tree / tab organization
- Allow grouping panes into a nested hierarchy (like a file tree).
- Each node can have content and children.
- Support drag-and-drop reorganization.
- Metadata per node must be synced across devices.

### F2. Pane metadata display
- Show creation time, update time, created-by device, updated-by device.
- Display non-intrusively — avoid crowding the UI.

---

## Already Done (do not redo)
- Silent marshal errors fixed (discovery.go, node.go, store.go, handlers.go)
- `interface{}` → `any` throughout
- HTTP status code constants (http.Status*)
- `clipboardMaxFileSize` constant in handlers
- Echo prevention bug fix (clipboard.go: clear lastWrittenText when user changes clipboard)
- Read deadline propagation in acceptSpokeConn and runSpokeConn
- `remarshal` helper replacing 11 double-marshal patterns
- Slow-client miss counter: tolerate 3 consecutive misses before eviction
- All 94 golangci-lint issues resolved (unused, staticcheck, gocritic, gosec, errcheck, cyclop, gocognit)
- store_test.go: 8 unit tests
- sync_test.go: 11 hub-spoke integration tests
- api_test.go: ~50+ handler/function tests added
- node_test.go: capBackoff, tlsErrorFilter, sendSSEState, hub handlers, setupSSE, ensureFileLocal fast-path, slow client miss counter, receiveClipboardFiles missing file, setupSSE non-Flusher 503
- store_test.go additions: NewStore, loadConfig, normalizeConfig, loadPanes, SSE, clipboard pane creation, monitor idempotency, prepareClipboardRecvDir, copyToRecvDir
- sync_test.go additions: TestAcceptSpokeConnBadJSONAuthMessage, TestAcceptSpokeConnEmptyDeviceID
- Production fixes: auth_fail sent synchronously (was dropped due to race), 503 for non-Flusher SSE

---

## Untestable (skip)
- UDP discovery functions
- Platform-specific clipboard (readClipboardImage*, writeClipboardImage*, readClipboardFiles*, writeClipboardFiles*)
- TLS cert generation
- Spoke connection management (runSpoke, connectToHub, spokeHandleFileNotify, spokeHandleClipboardUpdate)
- `forwardFileWithRetry` (5×backoff = 7.5s goroutine)
- `pollLoop` / `check` / `handleImageCheck` / `handleFileCheck` (require real system clipboard)
- `hubPinger` (10s ticker)
- `ensureFileLocal` retry paths (500ms sleep per retry × 3 loops)
