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
)

//go:embed web/*
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
		role := node.GetRole()
		status := map[string]interface{}{
			"role":         role,
			"deviceId":     node.store.config.DeviceID,
			"deviceName":   node.store.config.DeviceName,
			"token":        "",
			"hubAddr":      node.hubAddr,
			"devices":      node.getDevices(),
			"panes":        node.store.GetPanes(),
			"needsToken":   false,
			"authMode":     node.store.config.AuthMode,
			"authRequired": node.IsAuthRequired(),
		}
		if role == "hub" {
			status["token"] = node.token
		}
		if role == "spoke" && node.IsAuthRequired() && (normalizeToken(node.store.config.SavedToken) == "" || node.SpokeNeedsToken()) {
			status["needsToken"] = true
		}
		json.NewEncoder(w).Encode(status)
	})

	// Token
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		body.Token = normalizeToken(body.Token)
		if node.IsAuthRequired() && body.Token == "" {
			http.Error(w, "token required", 400)
			return
		}
		node.store.SetSavedToken(body.Token)
		node.setSpokeNeedsToken(false)
		// Force reconnect
		node.hubConnMu.Lock()
		if node.hubConn != nil {
			node.hubConn.Close()
		}
		node.hubConnMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
				pane.Type = PaneMarkdown
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
			node.store.DeletePane(id)
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

		// Spoke → forward to hub
		if node.GetRole() == "spoke" && node.hubAddr != "" {
			go forwardFile(node, storedName, header.Filename)
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
			// Spoke: try fetching from hub
			if node.GetRole() == "spoke" && node.hubAddr != "" {
				resp, err := http.Get(fmt.Sprintf("http://%s/api/files/%s", node.hubAddr, fileID))
				if err == nil && resp.StatusCode == 200 {
					defer resp.Body.Close()
					dst, err := os.Create(path)
					if err == nil {
						if _, err := io.Copy(dst, resp.Body); err != nil {
							dst.Close()
							os.Remove(path)
						} else {
							dst.Close()
							http.ServeFile(w, r, path)
							return
						}
					}
				}
			}
			http.Error(w, "file not found", 404)
			return
		}
		http.ServeFile(w, r, path)
	})

	return mux
}

func forwardFile(node *Node, storedName, fileName string) {
	f, err := os.Open(node.store.FilePath(storedName))
	if err != nil {
		return
	}
	defer f.Close()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return
	}
	io.Copy(part, f)
	writer.Close()
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/api/files?forceid=%s", node.hubAddr, storedName), body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	http.DefaultClient.Do(req)
}
