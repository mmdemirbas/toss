package main

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// eventually polls condition every 10 ms until it returns true or 2 s elapses.
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// hubServer creates a hub Node backed by an in-process HTTP test server.
// The server is closed via t.Cleanup.
func hubServer(t *testing.T) (*Node, *httptest.Server) {
	t.Helper()
	node := testNode(t)
	srv := httptest.NewServer(SetupHTTP(node))
	t.Cleanup(srv.Close)
	return node, srv
}

// connectSpoke dials hubSrv's /ws endpoint and runs the spoke read loop in a
// background goroutine.  The returned stop func disconnects cleanly; it is
// idempotent and also registered as t.Cleanup so callers need not call it
// explicitly unless they need an early disconnect.
func connectSpoke(t *testing.T, hubSrv *httptest.Server, deviceID, deviceName string) (*Node, func()) {
	t.Helper()

	store := testStore(t)
	store.config.DeviceID = deviceID
	store.config.DeviceName = deviceName

	node := NewNode(store, 0)
	node.roleMu.Lock()
	node.role = "spoke"
	node.roleMu.Unlock()

	wsURL := "ws" + strings.TrimPrefix(hubSrv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial hub ws: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = node.runSpokeConn(conn, true)
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = conn.Close()
			<-done
		})
	}
	t.Cleanup(stop)
	return node, stop
}

// waitForSpoke blocks until hub has registered deviceID as an authenticated client.
func waitForSpoke(t *testing.T, hub *Node, deviceID string) {
	t.Helper()
	eventually(t, func() bool {
		hub.clientsMu.RLock()
		defer hub.clientsMu.RUnlock()
		return hub.clients[deviceID] != nil
	})
}

// ---- Initial sync ----

func TestHubSpokeInitialSync(t *testing.T) {
	hub, hubSrv := hubServer(t)
	hub.store.UpsertPane(Pane{ID: "p1", Name: "Pane One", Content: "hello", Type: "code", Version: 1000})
	hub.store.UpsertPane(Pane{ID: "p2", Name: "Pane Two", Content: "world", Type: "code", Version: 1001})

	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")

	eventually(t, func() bool { return len(spoke.store.GetPanes()) == 2 })

	got := make(map[string]Pane)
	for _, p := range spoke.store.GetPanes() {
		got[p.ID] = p
	}
	if got["p1"].Content != "hello" {
		t.Errorf("p1: expected 'hello', got %q", got["p1"].Content)
	}
	if got["p2"].Content != "world" {
		t.Errorf("p2: expected 'world', got %q", got["p2"].Content)
	}
}

func TestSpokeReceivesEmptySyncWhenHubHasNoPanes(t *testing.T) {
	_, hubSrv := hubServer(t)
	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")

	// Spoke is connected; give it a moment then confirm no panes appeared.
	time.Sleep(100 * time.Millisecond)
	if n := len(spoke.store.GetPanes()); n != 0 {
		t.Errorf("expected 0 panes, got %d", n)
	}
}

// ---- Hub → spoke broadcast ----

func TestHubBroadcastsPaneUpdateToSpoke(t *testing.T) {
	hub, hubSrv := hubServer(t)
	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	waitForSpoke(t, hub, "spoke-001")

	pane := Pane{ID: "broadcast-pane", Content: "from hub", Type: "code", Version: nowMs()}
	hub.store.UpsertPane(pane)
	hub.broadcast(WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane}}, "")

	eventually(t, func() bool { return spoke.store.GetPane("broadcast-pane") != nil })

	got := spoke.store.GetPane("broadcast-pane")
	if got.Content != "from hub" {
		t.Errorf("expected 'from hub', got %q", got.Content)
	}
}

func TestHubBroadcastsPaneDeleteToSpoke(t *testing.T) {
	hub, hubSrv := hubServer(t)
	hub.store.UpsertPane(Pane{ID: "to-delete", Content: "exists", Type: "code", Version: 100})

	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	waitForSpoke(t, hub, "spoke-001")

	// Wait for the initial sync to land on the spoke.
	eventually(t, func() bool { return spoke.store.GetPane("to-delete") != nil })

	hub.store.DeletePane("to-delete")
	hub.broadcast(WSMessage{Type: "pane_delete", Payload: PaneDeletePayload{PaneID: "to-delete"}}, "")

	eventually(t, func() bool { return spoke.store.GetPane("to-delete") == nil })
}

// ---- Spoke → hub ----

func TestSpokeSendsPaneUpdateToHub(t *testing.T) {
	hub, hubSrv := hubServer(t)
	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	waitForSpoke(t, hub, "spoke-001")

	pane := Pane{ID: "spoke-pane", Content: "from spoke", Type: "code", Version: nowMs(), CreatedBy: "spoke-001"}
	if err := spoke.SendToHub(WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane}}); err != nil {
		t.Fatalf("SendToHub: %v", err)
	}

	eventually(t, func() bool { return hub.store.GetPane("spoke-pane") != nil })

	got := hub.store.GetPane("spoke-pane")
	if got.Content != "from spoke" {
		t.Errorf("expected 'from spoke', got %q", got.Content)
	}
}

func TestSpokeDeleteReachesHub(t *testing.T) {
	hub, hubSrv := hubServer(t)
	hub.store.UpsertPane(Pane{ID: "del-pane", Content: "exists", Type: "code", Version: 100})

	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	waitForSpoke(t, hub, "spoke-001")

	// Wait for spoke to receive the pane via sync before deleting it.
	eventually(t, func() bool { return spoke.store.GetPane("del-pane") != nil })

	if err := spoke.SendToHub(WSMessage{Type: "pane_delete", Payload: PaneDeletePayload{PaneID: "del-pane"}}); err != nil {
		t.Fatalf("SendToHub: %v", err)
	}

	eventually(t, func() bool { return hub.store.GetPane("del-pane") == nil })
}

// ---- Multi-spoke relay ----

func TestHubRelaysPaneUpdateBetweenSpokes(t *testing.T) {
	hub, hubSrv := hubServer(t)
	spoke1, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke1")
	spoke2, _ := connectSpoke(t, hubSrv, "spoke-002", "spoke2")
	waitForSpoke(t, hub, "spoke-001")
	waitForSpoke(t, hub, "spoke-002")

	pane := Pane{ID: "relay-pane", Content: "from spoke1", Type: "code", Version: nowMs(), CreatedBy: "spoke-001"}
	if err := spoke1.SendToHub(WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane}}); err != nil {
		t.Fatalf("spoke1 SendToHub: %v", err)
	}

	eventually(t, func() bool { return hub.store.GetPane("relay-pane") != nil })
	eventually(t, func() bool { return spoke2.store.GetPane("relay-pane") != nil })

	got := spoke2.store.GetPane("relay-pane")
	if got.Content != "from spoke1" {
		t.Errorf("spoke2: expected 'from spoke1', got %q", got.Content)
	}
}

func TestHubDoesNotEchoUpdateBackToSender(t *testing.T) {
	hub, hubSrv := hubServer(t)
	spoke1, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke1")
	spoke2, _ := connectSpoke(t, hubSrv, "spoke-002", "spoke2")
	waitForSpoke(t, hub, "spoke-001")
	waitForSpoke(t, hub, "spoke-002")

	pane := Pane{ID: "echo-pane", Content: "no echo", Type: "code", Version: nowMs()}
	if err := spoke1.SendToHub(WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane}}); err != nil {
		t.Fatalf("SendToHub: %v", err)
	}

	// spoke2 must receive it
	eventually(t, func() bool { return spoke2.store.GetPane("echo-pane") != nil })

	// spoke1 should NOT receive its own update back (the hub excludes the sender)
	time.Sleep(50 * time.Millisecond)
	if spoke1.store.GetPane("echo-pane") != nil {
		t.Error("hub should not echo the update back to the sender")
	}
}

// ---- Reconnect sync ----

func TestSpokeReconnectReceivesAllPanes(t *testing.T) {
	hub, hubSrv := hubServer(t)
	hub.store.UpsertPane(Pane{ID: "pre", Content: "before connect", Type: "code", Version: 100})

	// First connection: receive initial pane.
	spoke1, stop1 := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	eventually(t, func() bool { return spoke1.store.GetPane("pre") != nil })

	// Disconnect.
	stop1()

	// Hub gets a new pane while spoke is offline.
	hub.store.UpsertPane(Pane{ID: "post", Content: "added while offline", Type: "code", Version: 200})

	// Reconnect with a fresh spoke node (same device ID).
	spoke2, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")

	eventually(t, func() bool {
		return spoke2.store.GetPane("pre") != nil && spoke2.store.GetPane("post") != nil
	})

	if got := spoke2.store.GetPane("post"); got.Content != "added while offline" {
		t.Errorf("expected 'added while offline', got %q", got.Content)
	}
}

// ---- Version conflict via network ----

func TestHubDropsLowerVersionFromSpoke(t *testing.T) {
	hub, hubSrv := hubServer(t)
	hub.store.UpsertPane(Pane{ID: "conflict", Content: "hub version", Type: "code", Version: 2000})

	spoke, _ := connectSpoke(t, hubSrv, "spoke-001", "spoke")
	waitForSpoke(t, hub, "spoke-001")

	// Spoke sends an older version — hub must reject it.
	stale := Pane{ID: "conflict", Content: "stale spoke version", Type: "code", Version: 1999}
	if err := spoke.SendToHub(WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: stale}}); err != nil {
		t.Fatalf("SendToHub: %v", err)
	}

	// Give the hub time to process.
	time.Sleep(50 * time.Millisecond)

	got := hub.store.GetPane("conflict")
	if got == nil {
		t.Fatal("pane disappeared")
	}
	if got.Content != "hub version" || got.Version != 2000 {
		t.Errorf("hub accepted lower version: content=%q version=%d", got.Content, got.Version)
	}
}
