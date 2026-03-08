package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
)

const (
	clipboardPollInterval = 500 * time.Millisecond
	clipboardMaxTextSize  = 1 << 20  // 1 MB
	clipboardMaxImageSize = 10 << 20 // 10 MB
)

// ClipboardMonitor polls the system clipboard for changes and
// optionally creates panes and/or broadcasts updates to peers.
type ClipboardMonitor struct {
	node *Node

	mu                   sync.Mutex
	lastText             string
	lastWrittenText      string
	lastImageHash        string
	lastWrittenImageHash string
	imgCheckCounter      int
	running              bool
	stopCh               chan struct{}
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
		cm.lastText = content
	}
	// Seed image hash (may be slow, but runs once).
	if hash, _, _, err := readClipboardImage(); err == nil && hash != "" {
		cm.lastImageHash = hash
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

// check is called on every tick and handles both text and image clipboard.
func (cm *ClipboardMonitor) check() {
	text, _ := clipboard.ReadAll()

	cm.mu.Lock()
	textChanged := text != "" && text != cm.lastText && text != cm.lastWrittenText
	// Detect text→image transition: text goes from non-empty to empty.
	textCleared := text == "" && cm.lastText != ""
	cm.imgCheckCounter++
	periodicImageCheck := cm.imgCheckCounter%3 == 0 // every ~1.5 s
	cm.mu.Unlock()

	// ── Text changed ─────────────────────────────────────────────────
	if textChanged {
		cm.mu.Lock()
		cm.lastText = text
		cm.lastImageHash = "" // clipboard is now text
		cm.mu.Unlock()

		if len(text) > clipboardMaxTextSize {
			log.Printf("[clipboard] text too large (%d bytes), skipping", len(text))
			return
		}

		cfg := cm.node.store.GetClipboardConfig()
		if cfg.AutoTab {
			cm.node.createClipboardPane(text)
		}
		if cfg.SyncEnabled {
			cm.node.broadcastClipboard(text)
		}
		return
	}

	// ── Image check (on text-cleared or periodic) ────────────────────
	if textCleared || periodicImageCheck {
		cm.handleImageCheck(text)
	}
}

func (cm *ClipboardMonitor) handleImageCheck(currentText string) {
	imgHash, imgData, ext, err := readClipboardImage()
	if err != nil || imgHash == "" || len(imgData) == 0 {
		// No image – just track the text value.
		if currentText == "" {
			cm.mu.Lock()
			cm.lastText = ""
			cm.mu.Unlock()
		}
		return
	}

	cm.mu.Lock()
	if imgHash == cm.lastImageHash || imgHash == cm.lastWrittenImageHash {
		cm.lastText = currentText
		cm.mu.Unlock()
		return
	}
	cm.lastImageHash = imgHash
	cm.lastText = currentText
	cm.mu.Unlock()

	if len(imgData) > clipboardMaxImageSize {
		log.Printf("[clipboard] image too large (%d bytes), skipping", len(imgData))
		return
	}

	fileName := "clipboard-" + time.Now().Format("150405") + ext
	fileID, err := cm.node.storeImageData(imgData, ext)
	if err != nil {
		log.Printf("[clipboard] store image failed: %v", err)
		return
	}

	cfg := cm.node.store.GetClipboardConfig()
	if cfg.AutoTab {
		cm.node.createClipboardImagePane(fileID, fileName)
	}
	if cfg.SyncEnabled {
		cm.node.broadcastClipboardImage(fileID, fileName)
	}
	log.Printf("[clipboard] detected image (%d bytes, %s)", len(imgData), ext)
}

// WriteClipboard writes text received from a peer without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboard(content string) {
	cm.mu.Lock()
	cm.lastWrittenText = content
	cm.lastText = content
	cm.mu.Unlock()

	if err := clipboard.WriteAll(content); err != nil {
		log.Printf("[clipboard] write error: %v", err)
	} else {
		log.Printf("[clipboard] wrote %d bytes from peer", len(content))
	}
}

// WriteClipboardImage writes an image file to the clipboard without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboardImage(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("[clipboard] read image for clipboard failed: %v", err)
		return
	}
	hash := hashBytes(data)

	cm.mu.Lock()
	cm.lastWrittenImageHash = hash
	cm.lastImageHash = hash
	cm.lastText = "" // image on clipboard now
	cm.mu.Unlock()

	if err := writeClipboardImage(filePath); err != nil {
		log.Printf("[clipboard] write image error: %v", err)
	} else {
		log.Printf("[clipboard] wrote image to clipboard from peer (%d bytes)", len(data))
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
