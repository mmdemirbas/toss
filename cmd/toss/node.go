package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Shared TLS config that skips verification (self-signed certs between LAN peers)
var insecureTLSConfig = &tls.Config{InsecureSkipVerify: true}

var insecureHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: insecureTLSConfig,
	},
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Client struct {
	conn     *websocket.Conn
	device   Device
	sendCh   chan []byte
	mu       sync.Mutex
	authed   bool
	httpAddr string // "ip:port" of the client's HTTP server
}

type Node struct {
	store *Store
	port  int

	roleMu  sync.RWMutex
	role    string // "hub" or "spoke"
	hubAddr string
	hubID   string

	// Hub state
	clients     map[string]*Client
	clientsMu   sync.RWMutex
	collisionCh chan DiscoveryMsg
	reverseCh   chan DiscoveryMsg
	hubStopCh   chan struct{} // closed when hub should stop (demotion)

	// Spoke state
	hubConn   *websocket.Conn
	hubConnMu sync.Mutex
	spokeStop chan struct{}

	// Device tracking
	devices   map[string]Device
	devicesMu sync.RWMutex

	// SSE subscribers for push to browser
	sseMu   sync.Mutex
	sseSubs map[chan struct{}]struct{}

	// Clipboard monitor
	clipboard *ClipboardMonitor
}

func NewNode(store *Store, port int) *Node {
	n := &Node{
		store:       store,
		port:        port,
		clients:     make(map[string]*Client),
		devices:     make(map[string]Device),
		collisionCh: make(chan DiscoveryMsg, 4),
		reverseCh:   make(chan DiscoveryMsg, 8),
		hubStopCh:   make(chan struct{}),
		spokeStop:   make(chan struct{}),
		sseSubs:     make(map[chan struct{}]struct{}),
	}
	n.clipboard = NewClipboardMonitor(n)
	return n
}

func (n *Node) GetRole() string {
	n.roleMu.RLock()
	defer n.roleMu.RUnlock()
	return n.role
}

// Start performs discovery and starts in the appropriate role.
func (n *Node) Start() {
	log.Println("[node] discovering hub on LAN...")
	hubAddr, hubID, err := DiscoverHub(n.store.config.DeviceID, 3*time.Second)
	if err != nil {
		log.Printf("[node] discovery error: %v", err)
	}

	if hubAddr != "" {
		n.becomeSpoke(hubAddr, hubID)
	} else {
		n.becomeHub()
	}

	// Start clipboard monitor if any clipboard feature is enabled
	cfg := n.store.GetClipboardConfig()
	if cfg.AutoTab || cfg.SyncEnabled {
		n.clipboard.Start()
	}
}

func (n *Node) becomeHub() {
	n.roleMu.Lock()
	n.role = "hub"
	n.hubStopCh = make(chan struct{})
	n.roleMu.Unlock()

	self := Device{
		ID:       n.store.config.DeviceID,
		Name:     n.store.config.DeviceName,
		Role:     "hub",
		JoinedAt: nowMs(),
	}
	n.devicesMu.Lock()
	n.devices[self.ID] = self
	n.devicesMu.Unlock()

	log.Println("[node] running as HUB")

	go RunDiscoveryListener(n.store.config.DeviceID, n.port, n.collisionCh, n.reverseCh, n.hubStopCh)
	go AnnounceHub(n.store.config.DeviceID, n.port, n.hubStopCh)
	go n.handleHubCollisions()
	go n.handleReverseOffers()
}

func (n *Node) becomeSpoke(hubAddr, hubID string) {
	n.roleMu.Lock()
	n.role = "spoke"
	n.hubAddr = hubAddr
	n.hubID = hubID
	n.spokeStop = make(chan struct{})
	n.roleMu.Unlock()

	log.Printf("[node] running as SPOKE — hub at %s", hubAddr)
	go n.runSpoke()
}

// handleHubCollisions resolves dual-hub situations.
// Lower deviceID wins and stays hub; higher demotes to spoke.
func (n *Node) handleHubCollisions() {
	for {
		select {
		case <-n.hubStopCh:
			return
		case other := <-n.collisionCh:
			myID := n.store.config.DeviceID
			if other.DeviceID < myID {
				// Other hub has lower ID — they win, I demote
				hubAddr := fmt.Sprintf("%s:%d", other.IP, other.Port)
				log.Printf("[node] hub collision: %s wins over %s, demoting to spoke", other.DeviceID[:8], myID[:8])
				n.demoteToSpoke(hubAddr)
				return
			}
			// I have lower ID — I win, they should demote themselves
			log.Printf("[node] hub collision: I win (%s < %s), staying hub", myID[:8], other.DeviceID[:8])
		}
	}
}

func (n *Node) demoteToSpoke(hubAddr string) {
	// Stop hub services
	close(n.hubStopCh)

	// Disconnect all spoke clients gracefully
	n.clientsMu.Lock()
	for id, c := range n.clients {
		c.conn.Close()
		delete(n.clients, id)
	}
	n.clientsMu.Unlock()

	n.becomeSpoke(hubAddr, "")
	n.notifySSE()
}

func (n *Node) handleReverseOffers() {
	for {
		select {
		case <-n.hubStopCh:
			return
		case msg := <-n.reverseCh:
			n.tryReverseDial(msg)
		}
	}
}

func (n *Node) tryReverseDial(msg DiscoveryMsg) {
	if msg.IP == "" || msg.Port == 0 || msg.DeviceID == "" {
		return
	}
	n.clientsMu.RLock()
	_, exists := n.clients[msg.DeviceID]
	n.clientsMu.RUnlock()
	if exists {
		return
	}
	addr := fmt.Sprintf("%s:%d", msg.IP, msg.Port)
	url := "wss://" + addr + "/ws"
	log.Printf("[hub] attempting reverse dial to %s (%s)", addr, msg.DeviceID)
	dialer := websocket.Dialer{HandshakeTimeout: 4 * time.Second, TLSClientConfig: insecureTLSConfig}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return
	}
	if err := n.acceptSpokeConn(conn); err != nil {
		log.Printf("[hub] reverse dial failed: %v", err)
		conn.Close()
	}
}

// === Hub WebSocket Handler ===

func (n *Node) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	role := n.GetRole()
	if role != "hub" && role != "spoke" {
		http.Error(w, "node not ready", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	if role == "hub" {
		if err := n.acceptSpokeConn(conn); err != nil {
			conn.Close()
		}
		return
	}

	if err := n.runSpokeConn(conn, true); err != nil {
		conn.Close()
	}
}

func (n *Node) acceptSpokeConn(conn *websocket.Conn) error {
	client := &Client{conn: conn, sendCh: make(chan []byte, 64)}
	go n.clientWriter(client)

	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgData, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	var msg WSMessage
	if err := json.Unmarshal(msgData, &msg); err != nil || msg.Type != "auth" {
		n.sendToClient(client, WSMessage{Type: "auth_fail", Payload: map[string]string{"reason": "bad_auth_message"}})
		return fmt.Errorf("invalid auth message")
	}

	payloadData, _ := json.Marshal(msg.Payload)
	var auth AuthPayload
	json.Unmarshal(payloadData, &auth)
	if auth.DeviceID == "" {
		n.sendToClient(client, WSMessage{Type: "auth_fail", Payload: map[string]string{"reason": "bad_auth_payload"}})
		return fmt.Errorf("missing device id")
	}

	client.authed = true
	client.device = Device{ID: auth.DeviceID, Name: auth.DeviceName, Role: "spoke", JoinedAt: nowMs()}
	conn.SetReadDeadline(time.Time{})

	// Derive spoke HTTP address from WebSocket connection IP + auth port
	if auth.Port > 0 {
		remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		client.httpAddr = net.JoinHostPort(remoteIP, fmt.Sprintf("%d", auth.Port))
	}

	n.clientsMu.Lock()
	n.clients[auth.DeviceID] = client
	n.clientsMu.Unlock()

	n.devicesMu.Lock()
	n.devices[auth.DeviceID] = client.device
	n.devicesMu.Unlock()

	shortID := auth.DeviceID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	log.Printf("[hub] device %s (%s) connected", auth.DeviceName, shortID)

	n.sendToClient(client, WSMessage{Type: "auth_ok"})
	syncData := SyncPayload{Panes: n.store.GetPanes(), Devices: n.getDevices()}
	n.sendToClient(client, WSMessage{Type: "sync", Payload: syncData})
	n.broadcastDevices()
	n.notifySSE()

	// Fetch any files the hub is missing that the new spoke might have
	if client.httpAddr != "" {
		go n.fetchMissingPaneFiles([]string{client.httpAddr})
	}

	// Set up hub-side keepalive: detect dead spoke connections
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return nil
	})
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	go n.hubPinger(client)

	n.hubReadLoop(client)
	return nil
}

func (n *Node) hubReadLoop(client *Client) {
	defer func() {
		n.clientsMu.Lock()
		delete(n.clients, client.device.ID)
		n.clientsMu.Unlock()

		n.devicesMu.Lock()
		delete(n.devices, client.device.ID)
		n.devicesMu.Unlock()

		client.conn.Close()
		close(client.sendCh)
		log.Printf("[hub] device %s disconnected", client.device.Name)
		n.broadcastDevices()
		n.notifySSE()
	}()

	for {
		_, msgData, err := client.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg WSMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "pane_update":
			payloadData, _ := json.Marshal(msg.Payload)
			var payload PaneUpdatePayload
			json.Unmarshal(payloadData, &payload)
			payload.SenderID = client.device.ID
			n.store.UpsertPane(payload.Pane)
			n.broadcast(WSMessage{Type: "pane_update", Payload: payload}, client.device.ID)
			n.notifySSE()

		case "pane_delete":
			payloadData, _ := json.Marshal(msg.Payload)
			var payload PaneDeletePayload
			json.Unmarshal(payloadData, &payload)
			payload.SenderID = client.device.ID
			n.store.DeletePaneWithFiles(payload.PaneID)
			n.broadcast(WSMessage{Type: "pane_delete", Payload: payload}, client.device.ID)
			n.notifySSE()

		case "file_notify":
			payloadData, _ := json.Marshal(msg.Payload)
			var payload FileNotifyPayload
			json.Unmarshal(payloadData, &payload)
			payload.SenderID = client.device.ID
			// Hub: fetch the file from the spoke if we don't have it
			if payload.FileID != "" {
				go n.fetchFileFromAddr(payload.FileID, client.httpAddr)
			}
			// Relay to other spokes so they can pre-fetch on demand
			n.broadcast(WSMessage{Type: "file_notify", Payload: payload}, client.device.ID)

		case "clipboard_update":
			payloadData, _ := json.Marshal(msg.Payload)
			var payload ClipboardPayload
			json.Unmarshal(payloadData, &payload)
			payload.SenderID = client.device.ID
			// Relay to other spokes
			n.broadcast(WSMessage{Type: "clipboard_update", Payload: payload}, client.device.ID)
			// Write to hub's clipboard if sync enabled
			cfg := n.store.GetClipboardConfig()
			if cfg.SyncEnabled && n.clipboard != nil {
				if payload.ImageData != "" {
					if imgBytes, err := base64.StdEncoding.DecodeString(payload.ImageData); err == nil {
						n.clipboard.WriteClipboardImageData(imgBytes, payload.ImageExt)
					}
				} else if payload.Content != "" {
					n.clipboard.WriteClipboard(payload.Content)
				}
			}
		}
	}
}

func (n *Node) broadcast(msg WSMessage, excludeID string) {
	data, _ := json.Marshal(msg)
	n.clientsMu.RLock()
	defer n.clientsMu.RUnlock()
	for id, client := range n.clients {
		if id != excludeID && client.authed {
			select {
			case client.sendCh <- data:
			default:
				// Channel full — slow/dead client, force disconnect
				log.Printf("[hub] evicting slow client %s (broadcast)", client.device.Name)
				client.conn.Close()
			}
		}
	}
}

func (n *Node) broadcastDevices() {
	msg := WSMessage{Type: "devices", Payload: DevicesPayload{Devices: n.getDevices()}}
	n.broadcast(msg, "")
}

func (n *Node) getDevices() []Device {
	n.devicesMu.RLock()
	defer n.devicesMu.RUnlock()
	devs := make([]Device, 0, len(n.devices))
	for _, d := range n.devices {
		devs = append(devs, d)
	}
	return devs
}

// getSpokeAddrs returns HTTP addresses of all connected spokes.
func (n *Node) getSpokeAddrs() []string {
	n.clientsMu.RLock()
	defer n.clientsMu.RUnlock()
	addrs := make([]string, 0, len(n.clients))
	for _, c := range n.clients {
		if c.authed && c.httpAddr != "" {
			addrs = append(addrs, c.httpAddr)
		}
	}
	return addrs
}

func (n *Node) sendToClient(client *Client, msg WSMessage) {
	data, _ := json.Marshal(msg)
	select {
	case client.sendCh <- data:
	default:
		// Channel full — slow/dead client, force disconnect
		log.Printf("[hub] evicting slow client %s", client.device.Name)
		client.conn.Close()
	}
}

func (n *Node) clientWriter(client *Client) {
	for data := range client.sendCh {
		client.mu.Lock()
		client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := client.conn.WriteMessage(websocket.TextMessage, data)
		client.mu.Unlock()
		if err != nil {
			// Force-close so hubReadLoop unblocks and runs cleanup
			client.conn.Close()
			return
		}
	}
}

// hubPinger sends periodic pings to a spoke client to detect dead connections.
func (n *Node) hubPinger(client *Client) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.hubStopCh:
			return
		case <-ticker.C:
			client.mu.Lock()
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := client.conn.WriteMessage(websocket.PingMessage, nil)
			client.mu.Unlock()
			if err != nil {
				client.conn.Close()
				return
			}
		}
	}
}

// === Spoke Logic ===

func (n *Node) runSpoke() {
	backoff := 1 * time.Second
	maxBackoff := 15 * time.Second

	for {
		select {
		case <-n.spokeStop:
			return
		default:
		}

		if n.hasHubConn() {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		err := n.connectToHub()
		if err != nil {
			log.Printf("[spoke] connection lost: %v", err)
			if n.hubAddr != "" {
				SendReverseOffer(n.store.config.DeviceID, n.port, n.hubID)
			}
		}

		select {
		case <-n.spokeStop:
			return
		case <-time.After(backoff):
		}

		// Re-discover hub (it may have changed IP or another device became hub)
		log.Println("[spoke] re-discovering hub...")
		hubAddr, hubID, _ := DiscoverHub(n.store.config.DeviceID, 3*time.Second)
		if hubAddr != "" {
			n.roleMu.Lock()
			n.hubAddr = hubAddr
			n.hubID = hubID
			n.roleMu.Unlock()
			backoff = 1 * time.Second
			log.Printf("[spoke] found hub at %s", hubAddr)
		} else {
			// No hub found — maybe I should become hub
			log.Println("[spoke] no hub found, promoting to hub")
			n.becomeHub()
			n.notifySSE()
			return
		}

		if backoff < maxBackoff {
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (n *Node) connectToHub() error {
	n.roleMu.RLock()
	hubAddr := n.hubAddr
	n.roleMu.RUnlock()

	url := "wss://" + hubAddr + "/ws"
	log.Printf("[spoke] connecting to %s", url)

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig:  insecureTLSConfig,
	}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}
	return n.runSpokeConn(conn, true)
}

func (n *Node) hasHubConn() bool {
	n.hubConnMu.Lock()
	defer n.hubConnMu.Unlock()
	return n.hubConn != nil
}

func (n *Node) runSpokeConn(conn *websocket.Conn, sendAuth bool) error {
	// Send auth BEFORE exposing conn to other goroutines to prevent concurrent writes
	if sendAuth {
		auth := WSMessage{Type: "auth", Payload: AuthPayload{DeviceID: n.store.config.DeviceID, DeviceName: n.store.config.DeviceName, Port: n.port}}
		data, _ := json.Marshal(auth)
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			return err
		}
	}

	// Now safely publish the connection
	n.hubConnMu.Lock()
	if n.hubConn != nil && n.hubConn != conn {
		n.hubConn.Close()
	}
	n.hubConn = conn
	n.hubConnMu.Unlock()

	defer func() {
		n.hubConnMu.Lock()
		if n.hubConn == conn {
			n.hubConn = nil
		}
		n.hubConnMu.Unlock()
		conn.Close()
	}()

	// Set up keepalive pings with initial read deadline
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return nil
	})
	go n.spokePinger(conn)

	// Read loop
	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
		// Reset deadline on any successful read (hub may send data instead of pong)
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		var msg WSMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}
		n.handleSpokeMessage(msg)
	}
}

func (n *Node) spokePinger(conn *websocket.Conn) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-n.spokeStop:
			return
		case <-ticker.C:
			n.hubConnMu.Lock()
			if n.hubConn != conn {
				n.hubConnMu.Unlock()
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			n.hubConnMu.Unlock()
			if err != nil {
				conn.Close()
				return
			}
		}
	}
}

func (n *Node) handleSpokeMessage(msg WSMessage) {
	switch msg.Type {
	case "auth_ok":
		log.Println("[spoke] connected to hub")
		n.notifySSE()

	case "auth_fail":
		log.Println("[spoke] connection rejected by hub")
		n.notifySSE()

	case "sync":
		payloadData, _ := json.Marshal(msg.Payload)
		var s SyncPayload
		json.Unmarshal(payloadData, &s)
		n.store.ReplacePanes(s.Panes)
		n.devicesMu.Lock()
		n.devices = make(map[string]Device)
		for _, d := range s.Devices {
			n.devices[d.ID] = d
		}
		n.devicesMu.Unlock()
		log.Printf("[spoke] synced %d panes", len(s.Panes))
		n.notifySSE()
		// Fetch any files referenced in panes that we don't have locally
		go n.fetchMissingPaneFiles([]string{n.hubAddr})

	case "pane_update":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneUpdatePayload
		json.Unmarshal(payloadData, &payload)
		n.store.UpsertPane(payload.Pane)
		n.notifySSE()

	case "pane_delete":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneDeletePayload
		json.Unmarshal(payloadData, &payload)
		n.store.DeletePaneWithFiles(payload.PaneID)
		n.notifySSE()

	case "devices":
		payloadData, _ := json.Marshal(msg.Payload)
		var devPayload DevicesPayload
		json.Unmarshal(payloadData, &devPayload)
		n.devicesMu.Lock()
		n.devices = make(map[string]Device)
		for _, d := range devPayload.Devices {
			n.devices[d.ID] = d
		}
		n.devicesMu.Unlock()
		n.notifySSE()

	case "file_notify":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload FileNotifyPayload
		json.Unmarshal(payloadData, &payload)
		// Pre-fetch the file from hub if we don't have it locally
		if payload.FileID != "" {
			go n.fetchFileFromAddr(payload.FileID, n.hubAddr)
		}

	case "clipboard_update":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload ClipboardPayload
		json.Unmarshal(payloadData, &payload)
		// Write to local clipboard if sync enabled
		cfg := n.store.GetClipboardConfig()
		if cfg.SyncEnabled && n.clipboard != nil {
			if payload.ImageData != "" {
				if imgBytes, err := base64.StdEncoding.DecodeString(payload.ImageData); err == nil {
					n.clipboard.WriteClipboardImageData(imgBytes, payload.ImageExt)
				}
			} else if payload.Content != "" {
				n.clipboard.WriteClipboard(payload.Content)
			}
		}
	}
}

// fetchFileFromAddr fetches a file from a remote node's HTTP server if not already local.
func (n *Node) fetchFileFromAddr(fileID, addr string) {
	if addr == "" || fileID == "" {
		return
	}
	path := n.store.FilePath(fileID)
	if _, err := os.Stat(path); err == nil {
		return // already have it
	}
	url := fmt.Sprintf("https://%s/api/files/%s?norecurse=1", addr, fileID)
	resp, err := insecureHTTPClient.Get(url)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("[files] fetch %s from %s failed: %v", fileID, addr, err)
		return
	}
	defer resp.Body.Close()
	dst, err := os.Create(path)
	if err != nil {
		return
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		dst.Close()
		os.Remove(path)
		return
	}
	dst.Close()
	log.Printf("[files] fetched %s from %s", fileID, addr)
}

// fetchMissingPaneFiles scans all panes for file references and fetches any missing files.
func (n *Node) fetchMissingPaneFiles(addrs []string) {
	if len(addrs) == 0 {
		return
	}
	panes := n.store.GetPanes()
	var missing []string
	seen := make(map[string]bool)
	for _, p := range panes {
		matches := fileRefRe.FindAllStringSubmatch(p.Content, -1)
		for _, m := range matches {
			fileID := filepath.Base(m[1])
			if fileID == "" || fileID == "." || fileID == ".." || seen[fileID] {
				continue
			}
			seen[fileID] = true
			path := n.store.FilePath(fileID)
			if _, err := os.Stat(path); err == nil {
				continue // already have it
			}
			missing = append(missing, fileID)
		}
	}
	if len(missing) == 0 {
		return
	}
	log.Printf("[files] found %d missing file(s) referenced in panes, fetching...", len(missing))
	for _, fileID := range missing {
		for _, addr := range addrs {
			n.fetchFileFromAddr(fileID, addr)
			if _, err := os.Stat(n.store.FilePath(fileID)); err == nil {
				break // got it
			}
		}
	}
}

// createClipboardPane creates a new pane from clipboard text content.
// The pane is synced to all peers via pane_update (one tab everywhere).
func (n *Node) createClipboardPane(content string) {
	pane := Pane{
		ID:        generateID(),
		Name:      clipboardPaneName(content),
		Type:      "code",
		Content:   content,
		Language:  "plaintext",
		Order:     nowMs(),
		CreatedBy: n.store.config.DeviceID,
		CreatedAt: nowMs(),
		UpdatedAt: nowMs(),
		Version:   nowMs(),
	}
	n.store.UpsertPane(pane)
	update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: n.store.config.DeviceID}}
	if n.GetRole() == "hub" {
		n.broadcast(update, "")
	} else {
		n.SendToHub(update)
	}
	n.notifySSE()
	log.Printf("[clipboard] created pane: %s", pane.Name)
}

// createClipboardImagePane stores image bytes as a file, creates a markdown
// pane referencing it, and syncs both to peers (one tab everywhere).
func (n *Node) createClipboardImagePane(imgData []byte, ext, fileName string) {
	// Store the image file locally and forward to peers.
	fileID := generateID() + ext
	path := n.store.FilePath(fileID)
	if err := os.WriteFile(path, imgData, 0644); err != nil {
		log.Printf("[clipboard] store image failed: %v", err)
		return
	}
	log.Printf("[clipboard] stored image %s (%d bytes)", fileID, len(imgData))

	if n.GetRole() == "spoke" && n.hubAddr != "" {
		go forwardFileWithRetry(n, fileID, fileName)
	}
	if n.GetRole() == "hub" {
		n.broadcast(WSMessage{Type: "file_notify", Payload: FileNotifyPayload{
			FileID: fileID, FileName: fileName, SenderID: n.store.config.DeviceID,
		}}, "")
	}

	// Create a markdown pane with the image embedded.
	imgURL := "/api/files/" + fileID
	content := fmt.Sprintf("![📋 %s](%s)\n", fileName, imgURL)
	preview := true
	pane := Pane{
		ID:        generateID(),
		Name:      "📋 " + fileName,
		Type:      "code",
		Content:   content,
		Language:  "markdown",
		Preview:   &preview,
		Order:     nowMs(),
		CreatedBy: n.store.config.DeviceID,
		CreatedAt: nowMs(),
		UpdatedAt: nowMs(),
		Version:   nowMs(),
	}
	n.store.UpsertPane(pane)
	update := WSMessage{Type: "pane_update", Payload: PaneUpdatePayload{Pane: pane, SenderID: n.store.config.DeviceID}}
	if n.GetRole() == "hub" {
		n.broadcast(update, "")
	} else {
		n.SendToHub(update)
	}
	n.notifySSE()
	log.Printf("[clipboard] created image pane: %s", pane.Name)
}

// broadcastClipboardContent sends a self-contained clipboard_update to peers.
// For text the Content field carries the text; for images the ImageData field
// carries base64-encoded bytes so receivers can write to clipboard directly
// without fetching any files.
func (n *Node) broadcastClipboardContent(payload ClipboardPayload) {
	payload.SenderID = n.store.config.DeviceID
	msg := WSMessage{Type: "clipboard_update", Payload: payload}
	if n.GetRole() == "hub" {
		n.broadcast(msg, "")
	} else {
		n.SendToHub(msg)
	}
	if payload.ImageData != "" {
		log.Printf("[clipboard] sent clipboard image update (%d bytes encoded)", len(payload.ImageData))
	} else {
		log.Printf("[clipboard] sent clipboard update (%d bytes)", len(payload.Content))
	}
}

// SendToHub sends a message to the hub (spoke mode).
func (n *Node) SendToHub(msg WSMessage) error {
	n.hubConnMu.Lock()
	defer n.hubConnMu.Unlock()
	conn := n.hubConn
	if conn == nil {
		return fmt.Errorf("not connected to hub")
	}
	data, _ := json.Marshal(msg)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteMessage(websocket.TextMessage, data)
}

// SSE notification
func (n *Node) notifySSE() {
	n.sseMu.Lock()
	defer n.sseMu.Unlock()
	for ch := range n.sseSubs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (n *Node) subscribeSSE() chan struct{} {
	ch := make(chan struct{}, 1)
	n.sseMu.Lock()
	n.sseSubs[ch] = struct{}{}
	n.sseMu.Unlock()
	return ch
}

func (n *Node) unsubscribeSSE(ch chan struct{}) {
	n.sseMu.Lock()
	delete(n.sseSubs, ch)
	n.sseMu.Unlock()
}
