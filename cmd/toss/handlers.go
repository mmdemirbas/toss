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

	// Static files
	webSub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	// WebSocket
	mux.HandleFunc("/ws", node.HandleWebSocket)

	// Status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]interface{}{
			"role":       node.GetRole(),
			"deviceId":   node.store.config.DeviceID,
			"deviceName": node.store.config.DeviceName,
			"hubAddr":    node.hubAddr,
			"devices":    node.getDevices(),
			"panes":      node.store.GetPanes(),
			"clipboard":  node.store.GetClipboardConfig(),
		}
		json.NewEncoder(w).Encode(status)
	})

	// Panes CRUD
	mux.HandleFunc("/api/panes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(node.store.GetPanes())
		case "POST":
			var pane Pane
			if err := json.NewDecoder(r.Body).Decode(&pane); err != nil {
				http.Error(w, "invalid JSON", 400)
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
				node.SendToHub(update)
			}
			node.notifySSE()
			json.NewEncoder(w).Encode(pane)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	mux.HandleFunc("/api/panes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/panes/")
		if id == "" {
			http.Error(w, "missing id", 400)
			return
		}
		switch r.Method {
		case "PUT":
			var pane Pane
			if err := json.NewDecoder(r.Body).Decode(&pane); err != nil {
				http.Error(w, "invalid JSON", 400)
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
				node.SendToHub(update)
			}
			node.notifySSE()
			json.NewEncoder(w).Encode(pane)
		case "DELETE":
			node.store.DeletePaneWithFiles(id)
			del := WSMessage{Type: "pane_delete", Payload: PaneDeletePayload{PaneID: id, SenderID: node.store.config.DeviceID}}
			if node.GetRole() == "hub" {
				node.broadcast(del, "")
			} else {
				node.SendToHub(del)
			}
			node.notifySSE()
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	// File upload
	mux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.ParseMultipartForm(50 << 20)
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file required", 400)
			return
		}
		defer file.Close()

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
			http.Error(w, "storage error", 500)
			return
		}
		defer dst.Close()
		written, err := io.Copy(dst, file)
		if err != nil {
			http.Error(w, "write error", 500)
			return
		}

		// Spoke → forward to hub and notify via WS
		if node.GetRole() == "spoke" && node.hubAddr != "" {
			go forwardFileWithRetry(node, storedName, header.Filename)
		}
		// Hub → notify all spokes about the new file
		if node.GetRole() == "hub" {
			notify := WSMessage{Type: "file_notify", Payload: FileNotifyPayload{
				FileID:   storedName,
				FileName: header.Filename,
				SenderID: node.store.config.DeviceID,
			}}
			node.broadcast(notify, "")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"fileId":   storedName,
			"fileName": header.Filename,
			"mimeType": header.Header.Get("Content-Type"),
			"fileSize": written,
		})
		log.Printf("[files] stored %s (%s, %d bytes)", storedName, header.Filename, written)
	})

	// File download
	mux.HandleFunc("/api/files/", func(w http.ResponseWriter, r *http.Request) {
		fileID := strings.TrimPrefix(r.URL.Path, "/api/files/")
		if fileID == "" || fileID != filepath.Base(fileID) {
			http.Error(w, "invalid file id", 400)
			return
		}
		path := node.store.FilePath(fileID)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// Don't recurse: if a peer asked us, don't ask them back
			if r.URL.Query().Get("norecurse") == "1" {
				http.Error(w, "file not found", 404)
				return
			}
			// Try fetching from peers
			var addrs []string
			if node.GetRole() == "spoke" && node.hubAddr != "" {
				addrs = []string{node.hubAddr}
			} else if node.GetRole() == "hub" {
				addrs = node.getSpokeAddrs()
			}
			for _, addr := range addrs {
				resp, err := insecureHTTPClient.Get(fmt.Sprintf("https://%s/api/files/%s?norecurse=1", addr, fileID))
				if err != nil || resp.StatusCode != 200 {
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				dst, err := os.Create(path)
				if err == nil {
					_, copyErr := io.Copy(dst, resp.Body)
					resp.Body.Close()
					if copyErr != nil {
						dst.Close()
						os.Remove(path)
						continue
					}
					dst.Close()
					log.Printf("[files] fetched %s from %s", fileID, addr)
					http.ServeFile(w, r, path)
					return
				}
				resp.Body.Close()
			}
			http.Error(w, "file not found", 404)
			return
		}
		http.ServeFile(w, r, path)
	})

	// Clipboard config
	mux.HandleFunc("/api/clipboard/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(node.store.GetClipboardConfig())
		case "PUT":
			var cfg ClipboardConfig
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				http.Error(w, "invalid JSON", 400)
				return
			}
			node.store.SetClipboardConfig(cfg)
			go node.clipboard.Restart()
			node.notifySSE()
			json.NewEncoder(w).Encode(cfg)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	return mux
}

func forwardFileWithRetry(node *Node, storedName, fileName string) {
	const maxRetries = 5
	backoff := 500 * time.Millisecond

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := forwardFile(node, storedName, fileName)
		if err == nil {
			log.Printf("[files] forwarded %s to hub (attempt %d)", storedName, attempt)
			// Notify hub via WS as well (in case the HTTP forward was a race)
			node.SendToHub(WSMessage{Type: "file_notify", Payload: FileNotifyPayload{
				FileID:   storedName,
				FileName: fileName,
				SenderID: node.store.config.DeviceID,
			}})
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
	defer f.Close()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	writer.Close()
	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/api/files?forceid=%s", node.hubAddr, storedName), body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := insecureHTTPClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("hub returned %d", resp.StatusCode)
	}
	return nil
}
