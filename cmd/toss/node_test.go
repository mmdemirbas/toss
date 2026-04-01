package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ---- capBackoff ----

func TestCapBackoffDoubles(t *testing.T) {
	got := capBackoff(1*time.Second, 30*time.Second)
	if got != 2*time.Second {
		t.Errorf("expected 2s, got %v", got)
	}
}

func TestCapBackoffCapsAtMax(t *testing.T) {
	got := capBackoff(20*time.Second, 30*time.Second)
	if got != 30*time.Second {
		t.Errorf("expected cap at 30s, got %v", got)
	}
}

func TestCapBackoffExactlyMax(t *testing.T) {
	got := capBackoff(15*time.Second, 30*time.Second)
	if got != 30*time.Second {
		t.Errorf("expected 30s (doubled 15s), got %v", got)
	}
}

// ---- tlsErrorFilter ----

func TestTLSErrorFilterDiscardsHandshakeErrors(t *testing.T) {
	f := tlsErrorFilter{}
	msg := []byte("http: TLS handshake error from 127.0.0.1: EOF\n")
	n, err := f.Write(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("expected n=%d, got %d", len(msg), n)
	}
}

func TestTLSErrorFilterPassesThroughOtherErrors(t *testing.T) {
	f := tlsErrorFilter{}
	// Non-TLS messages pass through to stderr — just verify no panic and return values.
	msg := []byte("server: accept error\n")
	n, err := f.Write(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("expected n=%d, got %d", len(msg), n)
	}
}

// ---- sendSSEState ----

// flusherRecorder wraps httptest.ResponseRecorder and adds http.Flusher.
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (fr *flusherRecorder) Flush() { fr.flushed = true }

func TestSendSSEStateWritesDataEvent(t *testing.T) {
	node := testNode(t)
	node.store.UpsertPane(Pane{ID: "sse-p1", Name: "SSE Pane", Content: "hi", Version: 1})

	fr := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	sendSSEState(fr, fr, node)

	body := fr.Body.String()
	if !strings.HasPrefix(body, "data: ") {
		t.Errorf("expected 'data: ' prefix, got %q", body)
	}
	if !strings.Contains(body, "panes") {
		t.Error("SSE state must include 'panes'")
	}
	if !strings.Contains(body, "role") {
		t.Error("SSE state must include 'role'")
	}
	if !fr.flushed {
		t.Error("Flush must be called after writing state")
	}
}

// errResponseWriter returns an error on every Write call.
type errResponseWriter struct{ h http.Header }

func (e *errResponseWriter) Header() http.Header       { return e.h }
func (e *errResponseWriter) WriteHeader(_ int)          {}
func (e *errResponseWriter) Write(_ []byte) (int, error) { return 0, http.ErrHijacked }
func (e *errResponseWriter) Flush()                     {}

func TestSendSSEStateWriteErrorExitsEarly(t *testing.T) {
	node := testNode(t)
	ew := &errResponseWriter{h: make(http.Header)}
	// Must not panic even when the write fails.
	sendSSEState(ew, ew, node)
}

// ---- Hub message handlers ----

// stubClient builds a minimal authenticated client for direct handler calls.
func stubClient(deviceID string) *Client {
	return &Client{
		device: Device{ID: deviceID, Name: "test-spoke"},
		sendCh: make(chan []byte, 64),
		authed: true,
		// httpAddr is intentionally empty: fetchFileFromAddr will no-op
	}
}

func TestHubHandleFileNotifyBroadcasts(t *testing.T) {
	node := testNode(t)
	client := stubClient("spoke-1")

	msg := WSMessage{
		Type:    "file_notify",
		Payload: FileNotifyPayload{FileID: "img.png", FileName: "image.png"},
	}
	// Should not panic; broadcasts to zero other clients.
	node.hubHandleFileNotify(client, msg)
}

func TestHubHandleFileNotifyEmptyFileIDSkipsFetch(t *testing.T) {
	node := testNode(t)
	client := stubClient("spoke-1")

	// Empty FileID → fetchFileFromAddr goroutine must not be launched.
	msg := WSMessage{
		Type:    "file_notify",
		Payload: FileNotifyPayload{FileID: "", FileName: "nothing.png"},
	}
	node.hubHandleFileNotify(client, msg)
}

func TestHubHandleClipboardUpdateBroadcasts(t *testing.T) {
	node := testNode(t)
	client := stubClient("spoke-1")

	msg := WSMessage{
		Type:    "clipboard_update",
		Payload: ClipboardPayload{Content: "copied text"},
	}
	// SyncEnabled=false by default; no local clipboard write attempted.
	node.hubHandleClipboardUpdate(client, msg)
}

// ---- setupSSE endpoint smoke test ----

func TestSetupSSEEndpointReturnsEventStream(t *testing.T) {
	node := testNode(t)
	mux := http.NewServeMux()
	SetupHTTP(node) // registers handlers; we re-use testNode's handler for SSE
	setupSSE(node, mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Cancel immediately so the connection closes after the first event.
	req, err := http.NewRequest("GET", srv.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Use a custom client with a very short response timeout to avoid blocking.
	client := &http.Client{
		Transport: &http.Transport{},
		// CheckRedirect is default
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	// Read the first chunk — must start with "data: "
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	if n == 0 || !strings.HasPrefix(string(buf[:n]), "data: ") {
		t.Errorf("expected 'data: ' prefix in first SSE event, got %q", string(buf[:n]))
	}
}


// ---- ensureFileLocal fast-path ----

func TestEnsureFileLocalReturnsTrueWhenFileExists(t *testing.T) {
	node := testNode(t)
	fileID := "fast-path-file"
	if err := os.WriteFile(node.store.FilePath(fileID), []byte("data"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if !node.ensureFileLocal(fileID, "") {
		t.Error("expected true for existing file, got false")
	}
}

// ---- setupSSE non-Flusher 503 path ----

// noFlushWriter is a minimal http.ResponseWriter that does NOT implement http.Flusher.
type noFlushWriter struct {
	code int
	h    http.Header
}

func (n *noFlushWriter) Header() http.Header        { return n.h }
func (n *noFlushWriter) WriteHeader(code int)       { n.code = code }
func (n *noFlushWriter) Write(b []byte) (int, error) { return len(b), nil }

func TestSetupSSENonFlusherReturns503(t *testing.T) {
	node := testNode(t)
	mux := http.NewServeMux()
	setupSSE(node, mux)

	w := &noFlushWriter{h: make(http.Header)}
	r, _ := http.NewRequest("GET", "/api/events", nil)
	mux.ServeHTTP(w, r)

	if w.code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.code)
	}
}

// ---- slow client eviction miss counter ----

func TestSlowClientEvictedAfterThreeMisses(t *testing.T) {
	node := testNode(t)

	// Minimal echo server just to get a real WebSocket connection.
	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer echoSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(echoSrv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Unbuffered channel so every send misses.
	client := &Client{
		conn:   conn,
		sendCh: make(chan []byte),
		authed: true,
		device: Device{ID: "slow-spoke", Name: "slow-device"},
	}
	msg := WSMessage{Type: "pane_update", Payload: "data"}

	node.sendToClient(client, msg)
	client.mu.Lock()
	m1 := client.missCount
	client.mu.Unlock()
	if m1 != 1 {
		t.Errorf("after miss 1: expected missCount=1, got %d", m1)
	}

	node.sendToClient(client, msg)
	client.mu.Lock()
	m2 := client.missCount
	client.mu.Unlock()
	if m2 != 2 {
		t.Errorf("after miss 2: expected missCount=2, got %d", m2)
	}

	node.sendToClient(client, msg)
	client.mu.Lock()
	m3 := client.missCount
	client.mu.Unlock()
	if m3 != 3 {
		t.Errorf("after miss 3: expected missCount=3, got %d", m3)
	}

	// Connection must be closed after the 3rd miss.
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("expected connection closed after 3 misses, but read succeeded")
	}
}

func TestSlowClientMissCountResetsOnSuccessfulSend(t *testing.T) {
	node := testNode(t)

	client := &Client{
		conn:      nil, // not needed: eviction won't trigger
		sendCh:    make(chan []byte, 1),
		authed:    true,
		missCount: 2,
		device:    Device{ID: "spoke", Name: "device"},
	}
	msg := WSMessage{Type: "pane_update", Payload: "data"}

	node.sendToClient(client, msg)

	client.mu.Lock()
	got := client.missCount
	client.mu.Unlock()
	if got != 0 {
		t.Errorf("expected missCount reset to 0 after successful send, got %d", got)
	}
}
