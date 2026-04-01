package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed all:web
var webFS embed.FS

func SetupHTTP(node *Node) http.Handler {
	mux := http.NewServeMux()
	webSub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(webSub)))
	mux.HandleFunc("/ws", node.HandleWebSocket)
	mux.HandleFunc("/api/status", node.handleStatus)
	mux.HandleFunc("/api/panes", node.handlePanes)
	mux.HandleFunc("/api/panes/", node.handlePane)
	mux.HandleFunc("/api/files", node.handleFileUpload)
	mux.HandleFunc("/api/files/", node.handleFileDownload)
	mux.HandleFunc("/api/clipboard/config", node.handleClipboardConfig)
	return mux
}

func (node *Node) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]any{
		"role":       node.GetRole(),
		"deviceId":   node.store.config.DeviceID,
		"deviceName": node.store.config.DeviceName,
		"hubAddr":    node.hubAddr,
		"devices":    node.getDevices(),
		"panes":      node.store.GetPanes(),
		"clipboard":  node.store.GetClipboardConfig(),
	}
	if err := json.NewEncoder(w).Encode(status); err != nil {
		log.Printf("[api] encode status: %v", err)
	}
}

func (node *Node) handlePanes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case "GET":
		if err := json.NewEncoder(w).Encode(node.store.GetPanes()); err != nil {
			log.Printf("[api] encode panes: %v", err)
		}
	case "POST":
		node.handlePanesPost(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (node *Node) handlePanesPost(w http.ResponseWriter, r *http.Request) {
	var pane Pane
	if err := json.NewDecoder(r.Body).Decode(&pane); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if pane.ID == "" {
		pane.ID = generateID()
	}
	if pane.Type == "" {
		pane.Type = "code"
	}
	if pane.CreatedAt == 0 {
		pane.CreatedAt = nowMs()
	}
	if pane.Order == 0 {
		pane.Order = nowMs()
	}
	pane.UpdatedAt = nowMs()
	pane.Version = nowMs()
	if pane.CreatedBy == "" {
		pane.CreatedBy = node.store.config.DeviceID
	}
	node.store.UpsertPane(pane)
	update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: node.store.config.DeviceID}}
	if node.GetRole() == "hub" {
		node.broadcast(update, "")
	} else {
		if err := node.SendToHub(update); err != nil {
			log.Printf("[api] send to hub: %v", err)
		}
	}
	node.notifySSE()
	if err := json.NewEncoder(w).Encode(pane); err != nil {
		log.Printf("[api] encode pane: %v", err)
	}
}

func (node *Node) handlePane(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(r.URL.Path, "/api/panes/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case "PUT":
		node.handlePanePut(w, r, id)
	case "DELETE":
		node.handlePaneDelete(w, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (node *Node) handlePanePut(w http.ResponseWriter, r *http.Request, id string) {
	var pane Pane
	if err := json.NewDecoder(r.Body).Decode(&pane); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	pane.ID = id
	if pane.Order == 0 {
		if existing := node.store.GetPane(id); existing != nil {
			pane.Order = existing.Order
		}
		if pane.Order == 0 {
			pane.Order = nowMs()
		}
	}
	pane.UpdatedAt = nowMs()
	pane.Version = nowMs()
	node.store.UpsertPane(pane)
	update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: node.store.config.DeviceID}}
	if node.GetRole() == "hub" {
		node.broadcast(update, "")
	} else {
		if err := node.SendToHub(update); err != nil {
			log.Printf("[api] send to hub: %v", err)
		}
	}
	node.notifySSE()
	if err := json.NewEncoder(w).Encode(pane); err != nil {
		log.Printf("[api] encode pane: %v", err)
	}
}

func (node *Node) handlePaneDelete(w http.ResponseWriter, id string) {
	node.store.DeletePaneWithFiles(id)
	del := WSMessage{Type: "pane_delete", Payload: PaneDeletePayload{PaneID: id, SenderID: node.store.config.DeviceID}}
	if node.GetRole() == "hub" {
		node.broadcast(del, "")
	} else {
		if err := node.SendToHub(del); err != nil {
			log.Printf("[api] send to hub: %v", err)
		}
	}
	node.notifySSE()
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}); err != nil {
		log.Printf("[api] encode response: %v", err)
	}
}

func (node *Node) notifyFileStored(storedName, fileName string) {
	if node.GetRole() == "spoke" && node.hubAddr != "" {
		go forwardFileWithRetry(node, storedName, fileName)
		return
	}
	if node.GetRole() == "hub" {
		node.broadcast(WSMessage{Type: "file_notify", Payload: FileNotifyPayload{
			FileID:   storedName,
			FileName: fileName,
			SenderID: node.store.config.DeviceID,
		}}, "")
	}
}

func (node *Node) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, clipboardMaxFileSize)
	if err := r.ParseMultipartForm(clipboardMaxFileSize); err != nil {
		log.Printf("[files] parse multipart: %v", err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	ext := ""
	if idx := strings.LastIndex(header.Filename, "."); idx >= 0 {
		ext = header.Filename[idx:]
	}

	var storedName string
	if forceID := r.URL.Query().Get("forceid"); forceID != "" {
		storedName = filepath.Base(forceID)
	} else {
		storedName = generateID() + ext
	}

	dst, err := os.Create(node.store.FilePath(storedName))
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := dst.Close(); err != nil {
			log.Printf("[files] close dest: %v", err)
		}
	}()
	written, err := io.Copy(dst, file)
	if err != nil {
		http.Error(w, "write error", http.StatusInternalServerError)
		return
	}

	node.notifyFileStored(storedName, header.Filename)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"fileId":   storedName,
		"fileName": header.Filename,
		"mimeType": header.Header.Get("Content-Type"),
		"fileSize": written,
	}); err != nil {
		log.Printf("[files] encode response: %v", err)
	}
	log.Printf("[files] stored %s (%s, %d bytes)", storedName, header.Filename, written)
}

func (node *Node) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	fileID := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if fileID == "" || fileID != filepath.Base(fileID) {
		http.Error(w, "invalid file id", http.StatusBadRequest)
		return
	}
	path := node.store.FilePath(fileID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		node.fetchAndServeFile(w, r, fileID, path)
		return
	}
	http.ServeFile(w, r, path)
}

func (node *Node) peerAddrs() []string {
	if node.GetRole() == "spoke" && node.hubAddr != "" {
		return []string{node.hubAddr}
	}
	if node.GetRole() == "hub" {
		return node.getSpokeAddrs()
	}
	return nil
}

func fetchFileFromPeer(addr, fileID, path string) bool {
	resp, err := insecureHTTPClient.Get(fmt.Sprintf("https://%s/api/files/%s?norecurse=1", addr, fileID))
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return false
	}
	dst, err := os.Create(path)
	if err != nil {
		_ = resp.Body.Close()
		return false
	}
	_, copyErr := io.Copy(dst, resp.Body)
	_ = resp.Body.Close()
	if copyErr != nil {
		if err := dst.Close(); err != nil {
			log.Printf("[files] close dst: %v", err)
		}
		if err := os.Remove(path); err != nil {
			log.Printf("[files] remove partial: %v", err)
		}
		return false
	}
	if err := dst.Close(); err != nil {
		log.Printf("[files] close dst: %v", err)
	}
	log.Printf("[files] fetched %s from %s", fileID, addr)
	return true
}

func (node *Node) fetchAndServeFile(w http.ResponseWriter, r *http.Request, fileID, path string) {
	if r.URL.Query().Get("norecurse") == "1" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	for _, addr := range node.peerAddrs() {
		if fetchFileFromPeer(addr, fileID, path) {
			http.ServeFile(w, r, path)
			return
		}
	}
	http.Error(w, "file not found", http.StatusNotFound)
}

func (node *Node) handleClipboardConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case "GET":
		if err := json.NewEncoder(w).Encode(node.store.GetClipboardConfig()); err != nil {
			log.Printf("[api] encode clipboard config: %v", err)
		}
	case "PUT":
		var cfg ClipboardConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		node.store.SetClipboardConfig(cfg)
		go node.clipboard.Restart()
		node.notifySSE()
		if err := json.NewEncoder(w).Encode(cfg); err != nil {
			log.Printf("[api] encode clipboard config: %v", err)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func forwardFileWithRetry(node *Node, storedName, fileName string) {
	const maxRetries = 5
	backoff := 500 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := forwardFile(node, storedName, fileName)
		if err == nil {
			log.Printf("[files] forwarded %s to hub (attempt %d)", storedName, attempt)
			if err := node.SendToHub(WSMessage{Type: "file_notify", Payload: FileNotifyPayload{
				FileID:   storedName,
				FileName: fileName,
				SenderID: node.store.config.DeviceID,
			}}); err != nil {
				log.Printf("[files] notify hub: %v", err)
			}
			return
		}
		log.Printf("[files] forward %s failed (attempt %d/%d): %v", storedName, attempt, maxRetries, err)
		time.Sleep(backoff)
		backoff *= 2
	}
	log.Printf("[files] giving up forwarding %s after %d attempts", storedName, maxRetries)
}

func forwardFile(node *Node, storedName, fileName string) error {
	f, err := os.Open(node.store.FilePath(storedName))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/api/files?forceid=%s", node.hubAddr, storedName), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := insecureHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("hub returned %d", resp.StatusCode)
	}
	return nil
}
