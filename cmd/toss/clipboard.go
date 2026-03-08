package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
)

const (
	clipboardPollInterval = 500 * time.Millisecond
	clipboardMaxSize      = 1 << 20 // 1 MB
)

// ClipboardMonitor polls the system clipboard for changes and
// optionally creates panes and/or broadcasts updates to peers.
type ClipboardMonitor struct {
	node *Node

	mu          sync.Mutex
	lastContent string
	lastWritten string // content we wrote – used to suppress echo
	running     bool
	stopCh      chan struct{}
}

func NewClipboardMonitor(node *Node) *ClipboardMonitor {
	return &ClipboardMonitor{node: node}
}

// Start begins polling the clipboard. Safe to call multiple times.
func (cm *ClipboardMonitor) Start() {
	cm.mu.Lock()
	if cm.running {
		cm.mu.Unlock()
		return
	}
	cm.running = true
	cm.stopCh = make(chan struct{})

	// Seed with current clipboard content so we don't fire immediately.
	if content, err := clipboard.ReadAll(); err == nil {
		cm.lastContent = content
	}
	cm.mu.Unlock()

	go cm.pollLoop()
	log.Println("[clipboard] monitor started")
}

// Stop terminates the polling goroutine. Safe to call even when not running.
func (cm *ClipboardMonitor) Stop() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if !cm.running {
		return
	}
	cm.running = false
	close(cm.stopCh)
	log.Println("[clipboard] monitor stopped")
}

// Restart stops and (if config warrants it) restarts the monitor.
func (cm *ClipboardMonitor) Restart() {
	cm.Stop()
	time.Sleep(50 * time.Millisecond)
	cfg := cm.node.store.GetClipboardConfig()
	if cfg.AutoTab || cfg.SyncEnabled {
		cm.Start()
	}
}

func (cm *ClipboardMonitor) pollLoop() {
	ticker := time.NewTicker(clipboardPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.check()
		}
	}
}

func (cm *ClipboardMonitor) check() {
	content, err := clipboard.ReadAll()
	if err != nil {
		return
	}

	cm.mu.Lock()
	if content == cm.lastContent || content == "" {
		cm.mu.Unlock()
		return
	}
	// Avoid echoing content we just wrote on behalf of a peer.
	if content == cm.lastWritten {
		cm.lastContent = content
		cm.mu.Unlock()
		return
	}
	cm.lastContent = content
	cm.mu.Unlock()

	if len(content) > clipboardMaxSize {
		log.Printf("[clipboard] content too large (%d bytes), skipping", len(content))
		return
	}

	cfg := cm.node.store.GetClipboardConfig()

	if cfg.AutoTab {
		cm.node.createClipboardPane(content)
	}
	if cfg.SyncEnabled {
		cm.node.broadcastClipboard(content)
	}
}

// WriteClipboard writes content received from a peer without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboard(content string) {
	cm.mu.Lock()
	cm.lastWritten = content
	cm.lastContent = content
	cm.mu.Unlock()

	if err := clipboard.WriteAll(content); err != nil {
		log.Printf("[clipboard] write error: %v", err)
	} else {
		log.Printf("[clipboard] wrote %d bytes from peer", len(content))
	}
}

// clipboardPaneName derives a short pane name from clipboard text.
func clipboardPaneName(content string) string {
	first := strings.SplitN(strings.TrimSpace(content), "\n", 2)[0]
	first = strings.TrimSpace(first)
	if len([]rune(first)) > 40 {
		first = string([]rune(first)[:40]) + "…"
	}
	if first == "" {
		return fmt.Sprintf("📋 Clipboard %s", time.Now().Format("15:04:05"))
	}
	return "📋 " + first
}
