package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func SetupHTTP(node *Node) http.Handler {
	mux := http.NewServeMux()

	// WebSocket
	mux.HandleFunc("/ws", node.HandleWebSocket)

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
