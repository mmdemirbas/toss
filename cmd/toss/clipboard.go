package main

import (
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
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
// broadcasts updates to peers.
type ClipboardMonitor struct {
	node *Node

	mu                   sync.Mutex
	lastText             string
	lastWrittenText      string
	lastImageHash        string
	lastWrittenImageHash string
	lastFileHash         string
	lastWrittenFileHash  string
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
	// Seed file hash.
	if paths, err := readClipboardFiles(); err == nil && len(paths) > 0 {
		cm.lastFileHash = hashFilePaths(paths)
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

// Restart stops and restarts the monitor.
func (cm *ClipboardMonitor) Restart() {
	cm.Stop()
	time.Sleep(50 * time.Millisecond)
	cm.Start()
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
		cm.lastFileHash = ""  // clipboard is now text
		cm.mu.Unlock()

		if len(text) > clipboardMaxTextSize {
			log.Printf("[clipboard] text too large (%d bytes), skipping", len(text))
			return
		}

		cm.node.broadcastClipboardContent(ClipboardPayload{Content: text})
		return
	}

	// ── File or Image check (on text-cleared or periodic) ──────
	// Check files first: a copied file may also have an image preview
	// on the clipboard (macOS), so file detection takes priority.
	if textCleared || periodicImageCheck {
		if !cm.handleFileCheck(text) {
			cm.handleImageCheck(text)
		}
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
	cm.lastFileHash = "" // clipboard is now image
	cm.mu.Unlock()

	if len(imgData) > clipboardMaxImageSize {
		log.Printf("[clipboard] image too large (%d bytes), skipping", len(imgData))
		return
	}

	log.Printf("[clipboard] detected image (%d bytes, %s)", len(imgData), ext)

	// sync: send actual image content to peers (self-contained, no file fetch needed)
	cm.node.broadcastClipboardContent(ClipboardPayload{
		ImageData: base64.StdEncoding.EncodeToString(imgData),
		ImageExt:  ext,
	})
}

// WriteClipboard writes text received from a peer without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboard(content string) {
	cm.mu.Lock()
	cm.lastWrittenText = content
	cm.lastText = content
	cm.lastFileHash = ""  // clipboard is now text from peer
	cm.lastImageHash = "" // clipboard is now text from peer
	cm.mu.Unlock()

	if err := clipboard.WriteAll(content); err != nil {
		log.Printf("[clipboard] write error: %v", err)
	} else {
		log.Printf("[clipboard] wrote %d bytes from peer", len(content))
	}
}

// WriteClipboardImageData writes raw image bytes to the system clipboard
// without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboardImageData(imgData []byte, ext string) {
	hash := hashBytes(imgData)

	cm.mu.Lock()
	cm.lastWrittenImageHash = hash
	cm.lastImageHash = hash
	cm.lastText = ""     // image on clipboard now
	cm.lastFileHash = "" // clipboard is now image from peer
	cm.mu.Unlock()

	// Write to temp file, then to clipboard (OS tools require files).
	tmpFile := filepath.Join(os.TempDir(), "toss_sync_"+generateID()+ext)
	if err := os.WriteFile(tmpFile, imgData, 0644); err != nil {
		log.Printf("[clipboard] write temp image failed: %v", err)
		return
	}
	defer os.Remove(tmpFile)

	if err := writeClipboardImage(tmpFile); err != nil {
		log.Printf("[clipboard] write image error: %v", err)
	} else {
		log.Printf("[clipboard] wrote image to clipboard from peer (%d bytes)", len(imgData))
	}
}

// handleFileCheck detects file copies on the clipboard.
// Returns true if files are present (even if unchanged), preventing image check.
func (cm *ClipboardMonitor) handleFileCheck(currentText string) bool {
	paths, err := readClipboardFiles()
	if err != nil || len(paths) == 0 {
		return false
	}

	fileHash := hashFilePaths(paths)

	cm.mu.Lock()
	if fileHash == cm.lastFileHash || fileHash == cm.lastWrittenFileHash {
		cm.lastText = currentText
		cm.mu.Unlock()
		return true // files present but unchanged
	}
	cm.lastFileHash = fileHash
	cm.lastText = currentText
	cm.lastImageHash = "" // clipboard is now files, not image
	cm.mu.Unlock()

	// Filter: only regular files within size limits
	validPaths := cm.filterValidFiles(paths)
	if len(validPaths) == 0 {
		return true
	}

	log.Printf("[clipboard] detected %d file(s)", len(validPaths))

	// Store and forward files once
	files := cm.node.storeAndForwardFiles(validPaths)
	if len(files) == 0 {
		return true
	}

	cm.node.broadcastClipboardContent(ClipboardPayload{Files: files})
	return true
}

// filterValidFiles removes directories, too-large files, and enforces the max count.
func (cm *ClipboardMonitor) filterValidFiles(paths []string) []string {
	var valid []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			log.Printf("[clipboard] skipping %s: %v", filepath.Base(p), err)
			continue
		}
		if info.IsDir() {
			log.Printf("[clipboard] skipping directory: %s", filepath.Base(p))
			continue
		}
		if info.Size() > clipboardMaxFileSize {
			log.Printf("[clipboard] skipping %s: too large (%d bytes)", filepath.Base(p), info.Size())
			continue
		}
		valid = append(valid, p)
	}
	if len(valid) > clipboardMaxFileCount {
		log.Printf("[clipboard] too many files (%d), limiting to %d", len(valid), clipboardMaxFileCount)
		valid = valid[:clipboardMaxFileCount]
	}
	return valid
}

// WriteClipboardFiles writes file paths received from a peer without triggering an echo.
func (cm *ClipboardMonitor) WriteClipboardFiles(paths []string) {
	hash := hashFilePaths(paths)

	cm.mu.Lock()
	cm.lastWrittenFileHash = hash
	cm.lastFileHash = hash
	cm.lastText = ""      // files on clipboard now
	cm.lastImageHash = "" // clipboard is now files from peer
	cm.mu.Unlock()

	if err := writeClipboardFiles(paths); err != nil {
		log.Printf("[clipboard] write files error: %v", err)
	} else {
		log.Printf("[clipboard] wrote %d file(s) to clipboard from peer", len(paths))
	}
}
