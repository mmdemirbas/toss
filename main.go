package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 7753, "HTTP/WebSocket port")
	flag.Parse()

	log.SetFlags(log.Ltime | log.Lshortfile)

	store, err := NewStore()
	if err != nil {
		log.Fatalf("failed to init store: %v", err)
	}

	node := NewNode(store, *port)
	node.Start()

	handler := SetupHTTP(node)

	setupSSE(node, handler.(*http.ServeMux))

	url := fmt.Sprintf("http://localhost:%d", *port)
	fmt.Println()
	fmt.Println("  ╭─────────────────────────────────────────╮")
	fmt.Println("  │              L A N P A N E              │")
	fmt.Println("  ├─────────────────────────────────────────┤")
	if node.role == "hub" {
		fmt.Printf("  │  Role:  HUB                             │\n")
		fmt.Printf("  │  Code:  %-6s                           │\n", node.token)
	} else {
		fmt.Printf("  │  Role:  SPOKE                           │\n")
		fmt.Printf("  │  Hub:   %-20s          │\n", node.hubAddr)
	}
	fmt.Printf("  │  Open:  %-33s│\n", url)
	fmt.Printf("  │  OS:    %-10s                        │\n", runtime.GOOS)
	fmt.Println("  ╰─────────────────────────────────────────╯")
	fmt.Println()

	srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: handler}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		close(node.stopCh)
		srv.Close()
	}()

	log.Fatal(srv.ListenAndServe())
}

func setupSSE(node *Node, mux *http.ServeMux) {
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		lastVersion := int64(0)
		lastDevCount := 0
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			panes := node.store.GetPanes()
			devices := node.getDevices()

			currentVersion := int64(0)
			for _, p := range panes {
				if p.Version > currentVersion {
					currentVersion = p.Version
				}
			}

			if currentVersion != lastVersion || len(devices) != lastDevCount {
				lastVersion = currentVersion
				lastDevCount = len(devices)
				data := map[string]interface{}{
					"panes":   panes,
					"devices": devices,
				}
				jsonData, _ := json.Marshal(data)
				fmt.Fprintf(w, "data: %s\n\n", jsonData)
				flusher.Flush()
			}

			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	})
}
