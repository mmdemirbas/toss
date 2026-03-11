package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
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

	certFile, keyFile, err := ensureHTTPSCertFiles(store.dir)
	if err != nil {
		log.Fatalf("[tls] failed to setup certificates: %v", err)
	}

	fmt.Println()
	fmt.Println("  ╭─────────────────────────────────────────╮")
	fmt.Println("  │                 T O S S                 │")
	fmt.Println("  ├─────────────────────────────────────────┤")
	role := node.GetRole()
	if role == "hub" {
		fmt.Printf("  │  Role:  HUB                             │\n")
	} else {
		fmt.Printf("  │  Role:  SPOKE                           │\n")
		fmt.Printf("  │  Hub:   %-33s│\n", node.hubAddr)
	}
	fmt.Printf("  │  OS:    %-10s                        │\n", runtime.GOOS)
	fmt.Println("  ╰─────────────────────────────────────────╯")
	fmt.Println()

	srv := &http.Server{
		Addr:     fmt.Sprintf(":%d", *port),
		Handler:  handler,
		ErrorLog: log.New(tlsErrorFilter{}, "", log.Ltime|log.Lshortfile),
	}

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

// tlsErrorFilter suppresses expected TLS handshake errors logged by the HTTP
// server (e.g. browsers rejecting self-signed certs). These are not actionable
// and can be misleading, especially when the real issue is a peer being offline.
type tlsErrorFilter struct{}

func (tlsErrorFilter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "TLS handshake error") {
		return io.Discard.Write(p)
	}
	return os.Stderr.Write(p)
}
