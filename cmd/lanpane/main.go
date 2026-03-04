package main

import (
	"context"
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
	port := flag.Int("port", 7753, "HTTPS port")
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

	certFile, keyFile, err := ensureHTTPSCertFiles(store.dir)
	if err != nil {
		log.Fatalf("[tls] failed to setup certificates: %v", err)
	}

	url := fmt.Sprintf("https://localhost:%d", *port)
	fmt.Println()
	fmt.Println("  ╭─────────────────────────────────────────╮")
	fmt.Println("  │              L A N P A N E              │")
	fmt.Println("  ├─────────────────────────────────────────┤")
	role := node.GetRole()
	if role == "hub" {
		fmt.Printf("  │  Role:  HUB                             │\n")
	} else {
		fmt.Printf("  │  Role:  SPOKE                           │\n")
		fmt.Printf("  │  Hub:   %-33s│\n", node.hubAddr)
	}
	fmt.Printf("  │  Open:  %-33s│\n", url)
	fmt.Printf("  │  OS:    %-10s                        │\n", runtime.GOOS)
	fmt.Println("  ╰─────────────────────────────────────────╯")
	fmt.Println()

	srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: handler}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	log.Printf("[server] listening on https://:%d", *port)

	select {
	case sig := <-sigCh:
		log.Printf("shutting down on signal: %v", sig)
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
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

		ch := node.subscribeSSE()
		defer node.unsubscribeSSE(ch)

		// Send initial state immediately
		sendSSEState(w, flusher, node)

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				sendSSEState(w, flusher, node)
			case <-time.After(15 * time.Second):
				// Heartbeat to detect dead connections
				fmt.Fprintf(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	})
}

func sendSSEState(w http.ResponseWriter, flusher http.Flusher, node *Node) {
	data := map[string]interface{}{
		"panes":   node.store.GetPanes(),
		"devices": node.getDevices(),
		"role":    node.GetRole(),
	}
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()
}
