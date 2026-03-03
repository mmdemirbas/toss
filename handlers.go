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
	"strings"
)

//go:embed web/*
var webFS embed.FS

func SetupHTTP(node *Node) http.Handler {
	mux := http.NewServeMux()

	// Static files
	webSub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(webSub))
	mux.Handle("/", fileServer)

	// WebSocket (hub only, but we register on both—spokes just won't get connections)
	mux.HandleFunc("/ws", node.HandleWebSocket)

	// API: status
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := map[string]interface{}{
			"role":       node.role,
			"deviceId":   node.store.config.DeviceID,
			"deviceName": node.store.config.DeviceName,
			"token":      "",
			"hubAddr":    node.hubAddr,
			"devices":    node.getDevices(),
			"panes":      node.store.GetPanes(),
			"needsToken": false,
		}
		if node.role == "hub" {
			status["token"] = node.token
		}
		if node.role == "spoke" && node.store.config.SavedToken == "" {
			status["needsToken"] = true
		}
		json.NewEncoder(w).Encode(status)
	})

	// API: set token (spoke submits token)
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		body.Token = strings.TrimSpace(strings.ToUpper(body.Token))
		if body.Token == "" {
			http.Error(w, "token required", 400)
			return
		}
		node.store.SetSavedToken(body.Token)
		// Reconnect with new token
		node.hubConnMu.Lock()
		if node.hubConn != nil {
			node.hubConn.Close()
		}
		node.hubConnMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// API: panes CRUD
	mux.HandleFunc("/api/panes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(node.store.GetPanes())
		case "POST":
			var pane Pane
			json.NewDecoder(r.Body).Decode(&pane)
			if pane.ID == "" {
				pane.ID = generateID()
			}
			if pane.CreatedAt == 0 {
				pane.CreatedAt = nowMs()
			}
			pane.UpdatedAt = nowMs()
			pane.Version = nowMs()
			if pane.CreatedBy == "" {
				pane.CreatedBy = node.store.config.DeviceID
			}
			if pane.Blocks == nil {
				pane.Blocks = []Block{}
			}
			node.store.UpsertPane(pane)
			// Broadcast
			update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: node.store.config.DeviceID}}
			if node.role == "hub" {
				node.broadcast(update, "")
			} else {
				node.SendToHub(update)
			}
			json.NewEncoder(w).Encode(pane)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	mux.HandleFunc("/api/panes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/api/panes/")
		if id == "" {
			http.Error(w, "missing pane id", 400)
			return
		}

		switch r.Method {
		case "PUT":
			var pane Pane
			json.NewDecoder(r.Body).Decode(&pane)
			pane.ID = id
			pane.UpdatedAt = nowMs()
			pane.Version = nowMs()
			node.store.UpsertPane(pane)
			update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: node.store.config.DeviceID}}
			if node.role == "hub" {
				node.broadcast(update, "")
			} else {
				node.SendToHub(update)
			}
			json.NewEncoder(w).Encode(pane)

		case "DELETE":
			node.store.DeletePane(id)
			del := WSMessage{Type: "pane_delete", Payload: PaneDeletePayload{PaneID: id, SenderID: node.store.config.DeviceID}}
			if node.role == "hub" {
				node.broadcast(del, "")
			} else {
				node.SendToHub(del)
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	// API: file upload
	mux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.ParseMultipartForm(50 << 20) // 50MB max
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
		// If forwarded from spoke, use the same file ID
		if forceID := r.URL.Query().Get("forceid"); forceID != "" {
			storedName = forceID
		} else {
			storedName = generateID() + ext
		}

		dst, err := os.Create(node.store.FilePath(storedName))
		if err != nil {
			http.Error(w, "storage error", 500)
			return
		}
		defer dst.Close()
		written, _ := io.Copy(dst, file)

		// If spoke, forward file to hub so other spokes can fetch it
		if node.role == "spoke" && node.hubAddr != "" {
			go func() {
				f, err := os.Open(node.store.FilePath(storedName))
				if err != nil {
					return
				}
				defer f.Close()
				body := &bytes.Buffer{}
				writer := multipart.NewWriter(body)
				part, err := writer.CreateFormFile("file", header.Filename)
				if err != nil {
					return
				}
				io.Copy(part, f)
				writer.Close()
				req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/api/files?forceid=%s", node.hubAddr, storedName), body)
				req.Header.Set("Content-Type", writer.FormDataContentType())
				http.DefaultClient.Do(req)
			}()
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

	// API: file download
	mux.HandleFunc("/api/files/", func(w http.ResponseWriter, r *http.Request) {
		fileID := strings.TrimPrefix(r.URL.Path, "/api/files/")
		if fileID == "" {
			http.Error(w, "missing file id", 400)
			return
		}
		path := node.store.FilePath(fileID)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// If spoke, try fetching from hub
			if node.role == "spoke" && node.hubAddr != "" {
				resp, err := http.Get(fmt.Sprintf("http://%s/api/files/%s", node.hubAddr, fileID))
				if err == nil && resp.StatusCode == 200 {
					defer resp.Body.Close()
					// Cache locally
					dst, _ := os.Create(path)
					io.Copy(dst, resp.Body)
					dst.Close()
					http.ServeFile(w, r, path)
					return
				}
			}
			http.Error(w, "file not found", 404)
			return
		}
		http.ServeFile(w, r, path)
	})

	return mux
}
