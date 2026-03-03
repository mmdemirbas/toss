package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Client struct {
	conn   *websocket.Conn
	device Device
	sendCh chan []byte
	mu     sync.Mutex
	authed bool
}

type Node struct {
	store *Store
	port  int

	roleMu  sync.RWMutex
	role    string // "hub" or "spoke"
	token   string
	hubAddr string

	// Hub state
	clients     map[string]*Client
	clientsMu   sync.RWMutex
	collisionCh chan DiscoveryMsg
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
}

func NewNode(store *Store, port int) *Node {
	return &Node{
		store:       store,
		port:        port,
		clients:     make(map[string]*Client),
		devices:     make(map[string]Device),
		collisionCh: make(chan DiscoveryMsg, 4),
		hubStopCh:   make(chan struct{}),
		spokeStop:   make(chan struct{}),
		sseSubs:     make(map[chan struct{}]struct{}),
	}
}

func (n *Node) GetRole() string {
	n.roleMu.RLock()
	defer n.roleMu.RUnlock()
	return n.role
}

// Start performs discovery and starts in the appropriate role.
func (n *Node) Start() {
	log.Println("[node] discovering hub on LAN...")
	hubAddr, _, err := DiscoverHub(n.store.config.DeviceID, 3*time.Second)
	if err != nil {
		log.Printf("[node] discovery error: %v", err)
	}

	if hubAddr != "" {
		n.becomeSpoke(hubAddr)
	} else {
		n.becomeHub()
	}
}

func (n *Node) becomeHub() {
	n.roleMu.Lock()
	n.role = "hub"
	if n.store.config.Token == "" {
		n.token = generateToken()
		n.store.SetToken(n.token)
	} else {
		n.token = n.store.config.Token
	}
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

	log.Printf("[node] running as HUB — code: %s", n.token)

	go RunDiscoveryListener(n.store.config.DeviceID, n.port, n.collisionCh, n.hubStopCh)
	go AnnounceHub(n.store.config.DeviceID, n.port, n.hubStopCh)
	go n.handleHubCollisions()
}

func (n *Node) becomeSpoke(hubAddr string) {
	n.roleMu.Lock()
	n.role = "spoke"
	n.hubAddr = hubAddr
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

	n.becomeSpoke(hubAddr)
	n.notifySSE()
}

// === Hub WebSocket Handler ===

func (n *Node) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	if n.GetRole() != "hub" {
		http.Error(w, "not a hub", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &Client{
		conn:   conn,
		sendCh: make(chan []byte, 64),
	}
	go n.clientWriter(client)

	// Wait for auth (30s deadline)
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgData, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return
	}

	var msg WSMessage
	if err := json.Unmarshal(msgData, &msg); err != nil || msg.Type != "auth" {
		n.sendToClient(client, WSMessage{Type: "auth_fail"})
		conn.Close()
		return
	}

	payloadData, _ := json.Marshal(msg.Payload)
	var auth AuthPayload
	json.Unmarshal(payloadData, &auth)

	if auth.Token != n.token {
		n.sendToClient(client, WSMessage{Type: "auth_fail"})
		time.Sleep(100 * time.Millisecond)
		conn.Close()
		return
	}

	client.authed = true
	client.device = Device{
		ID:       auth.DeviceID,
		Name:     auth.DeviceName,
		Role:     "spoke",
		JoinedAt: nowMs(),
	}
	conn.SetReadDeadline(time.Time{})

	n.clientsMu.Lock()
	n.clients[auth.DeviceID] = client
	n.clientsMu.Unlock()

	n.devicesMu.Lock()
	n.devices[auth.DeviceID] = client.device
	n.devicesMu.Unlock()

	log.Printf("[hub] device %s (%s) connected", auth.DeviceName, auth.DeviceID[:8])

	// Send auth ok + full sync
	n.sendToClient(client, WSMessage{Type: "auth_ok"})
	syncData := SyncPayload{
		Panes:   n.store.GetPanes(),
		Devices: n.getDevices(),
	}
	n.sendToClient(client, WSMessage{Type: "sync", Payload: syncData})
	n.broadcastDevices()
	n.notifySSE()

	// Read loop
	n.hubReadLoop(client)
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
			n.store.DeletePane(payload.PaneID)
			n.broadcast(WSMessage{Type: "pane_delete", Payload: payload}, client.device.ID)
			n.notifySSE()
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

func (n *Node) sendToClient(client *Client, msg WSMessage) {
	data, _ := json.Marshal(msg)
	select {
	case client.sendCh <- data:
	default:
	}
}

func (n *Node) clientWriter(client *Client) {
	for data := range client.sendCh {
		client.mu.Lock()
		err := client.conn.WriteMessage(websocket.TextMessage, data)
		client.mu.Unlock()
		if err != nil {
			return
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

		err := n.connectToHub()
		if err != nil {
			log.Printf("[spoke] connection lost: %v", err)
		}

		select {
		case <-n.spokeStop:
			return
		case <-time.After(backoff):
		}

		// Re-discover hub (it may have changed IP or another device became hub)
		log.Println("[spoke] re-discovering hub...")
		hubAddr, _, _ := DiscoverHub(n.store.config.DeviceID, 3*time.Second)
		if hubAddr != "" {
			n.roleMu.Lock()
			n.hubAddr = hubAddr
			n.roleMu.Unlock()
			backoff = 1 * time.Second
			log.Printf("[spoke] found hub at %s", hubAddr)
		} else {
			// No hub found — maybe I should become hub
			log.Println("[spoke] no hub found, promoting to hub")
			n.roleMu.Lock()
			n.role = "hub"
			n.roleMu.Unlock()
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

	url := "ws://" + hubAddr + "/ws"
	log.Printf("[spoke] connecting to %s", url)

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return err
	}

	n.hubConnMu.Lock()
	n.hubConn = conn
	n.hubConnMu.Unlock()

	defer func() {
		n.hubConnMu.Lock()
		n.hubConn = nil
		n.hubConnMu.Unlock()
		conn.Close()
	}()

	// Set up keepalive pings
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return nil
	})
	go n.spokePinger(conn)

	// Auth
	token := n.store.config.SavedToken
	auth := WSMessage{
		Type: "auth",
		Payload: AuthPayload{
			Token:      token,
			DeviceID:   n.store.config.DeviceID,
			DeviceName: n.store.config.DeviceName,
		},
	}
	data, _ := json.Marshal(auth)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}

	// Read loop
	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
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
			err := conn.WriteMessage(websocket.PingMessage, nil)
			n.hubConnMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (n *Node) handleSpokeMessage(msg WSMessage) {
	switch msg.Type {
	case "auth_ok":
		log.Println("[spoke] authenticated")
		n.notifySSE()

	case "auth_fail":
		log.Println("[spoke] auth failed — bad token")
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
		n.store.DeletePane(payload.PaneID)
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
	}
}

// SendToHub sends a message to the hub (spoke mode).
func (n *Node) SendToHub(msg WSMessage) error {
	n.hubConnMu.Lock()
	conn := n.hubConn
	n.hubConnMu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected to hub")
	}
	data, _ := json.Marshal(msg)
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
