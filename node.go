package main

import (
	"encoding/json"
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
	conn     *websocket.Conn
	device   Device
	sendCh   chan []byte
	mu       sync.Mutex
	authed   bool
}

type Node struct {
	store    *Store
	role     string // "hub" or "spoke"
	token    string
	port     int
	hubAddr  string // only for spoke: "ip:port" of hub

	// Hub state
	clients   map[string]*Client
	clientsMu sync.RWMutex
	stopCh    chan struct{}

	// Spoke state
	hubConn   *websocket.Conn
	hubConnMu sync.Mutex

	// Device tracking
	devices   map[string]Device
	devicesMu sync.RWMutex
}

func NewNode(store *Store, port int) *Node {
	return &Node{
		store:   store,
		port:    port,
		clients: make(map[string]*Client),
		devices: make(map[string]Device),
		stopCh:  make(chan struct{}),
	}
}

// Start performs discovery and starts in the appropriate role.
func (n *Node) Start() {
	log.Println("[node] discovering hub on LAN...")
	hubAddr, err := DiscoverHub(n.store.config.DeviceID, 2*time.Second)
	if err != nil {
		log.Printf("[node] discovery error: %v", err)
	}

	if hubAddr != "" {
		n.role = "spoke"
		n.hubAddr = hubAddr
		log.Printf("[node] found hub at %s, connecting as spoke", hubAddr)
		go n.runSpoke()
	} else {
		n.role = "hub"
		// Generate or reuse token
		if n.store.config.Token == "" {
			n.token = generateToken()
			n.store.SetToken(n.token)
		} else {
			n.token = n.store.config.Token
		}
		log.Printf("[node] no hub found. Starting as hub.")
		log.Printf("[node] Access Code: %s", n.token)

		// Register self as device
		self := Device{
			ID:       n.store.config.DeviceID,
			Name:     n.store.config.DeviceName,
			Role:     "hub",
			JoinedAt: nowMs(),
		}
		n.devicesMu.Lock()
		n.devices[self.ID] = self
		n.devicesMu.Unlock()

		go RunDiscoveryListener(n.store.config.DeviceID, n.port, n.stopCh)
	}
}

// === Hub Logic ===

func (n *Node) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[hub] websocket upgrade error: %v", err)
		return
	}

	client := &Client{
		conn:   conn,
		sendCh: make(chan []byte, 64),
	}

	// Start writer goroutine
	go n.clientWriter(client)

	// Wait for auth
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	_, msgData, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return
	}

	var msg WSMessage
	if err := json.Unmarshal(msgData, &msg); err != nil || msg.Type != "auth" {
		n.sendMsg(client, WSMessage{Type: "auth_fail"})
		conn.Close()
		return
	}

	payloadData, _ := json.Marshal(msg.Payload)
	var auth AuthPayload
	json.Unmarshal(payloadData, &auth)

	if auth.Token != n.token {
		n.sendMsg(client, WSMessage{Type: "auth_fail"})
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
	conn.SetReadDeadline(time.Time{}) // clear deadline

	n.clientsMu.Lock()
	n.clients[auth.DeviceID] = client
	n.clientsMu.Unlock()

	n.devicesMu.Lock()
	n.devices[auth.DeviceID] = client.device
	n.devicesMu.Unlock()

	log.Printf("[hub] device %s (%s) connected", auth.DeviceName, auth.DeviceID)

	// Send auth success then full sync
	n.sendMsg(client, WSMessage{Type: "auth_ok"})
	sync := SyncPayload{
		Panes:   n.store.GetPanes(),
		Devices: n.getDevices(),
	}
	n.sendMsg(client, WSMessage{Type: "sync", Payload: sync})

	// Broadcast device list
	n.broadcastDevices()

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
		n.handleHubMessage(client, msg, msgData)
	}
}

func (n *Node) handleHubMessage(sender *Client, msg WSMessage, raw []byte) {
	switch msg.Type {
	case "pane_update":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneUpdatePayload
		json.Unmarshal(payloadData, &payload)
		payload.SenderID = sender.device.ID
		n.store.UpsertPane(payload.Pane)
		// Relay to all other clients
		relay := WSMessage{Type: "pane_update", Payload: payload}
		n.broadcast(relay, sender.device.ID)

	case "pane_delete":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneDeletePayload
		json.Unmarshal(payloadData, &payload)
		payload.SenderID = sender.device.ID
		n.store.DeletePane(payload.PaneID)
		relay := WSMessage{Type: "pane_delete", Payload: payload}
		n.broadcast(relay, sender.device.ID)
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
				log.Printf("[hub] dropping message for slow client %s", id)
			}
		}
	}
}

func (n *Node) broadcastDevices() {
	msg := WSMessage{Type: "devices", Payload: DevicesPayload{Devices: n.getDevices()}}
	data, _ := json.Marshal(msg)
	n.clientsMu.RLock()
	defer n.clientsMu.RUnlock()
	for _, client := range n.clients {
		if client.authed {
			select {
			case client.sendCh <- data:
			default:
			}
		}
	}
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

func (n *Node) sendMsg(client *Client, msg WSMessage) {
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
	for {
		err := n.connectToHub()
		if err != nil {
			log.Printf("[spoke] connection error: %v, retrying in 3s...", err)
			time.Sleep(3 * time.Second)
			// Re-discover in case hub moved
			hubAddr, _ := DiscoverHub(n.store.config.DeviceID, 2*time.Second)
			if hubAddr != "" {
				n.hubAddr = hubAddr
			}
			continue
		}
	}
}

func (n *Node) connectToHub() error {
	url := "ws://" + n.hubAddr + "/ws"
	log.Printf("[spoke] connecting to %s", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
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

	// Authenticate
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

	// Read messages
	for {
		_, msgData, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg WSMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}
		n.handleSpokeMessage(msg)
	}
}

func (n *Node) handleSpokeMessage(msg WSMessage) {
	switch msg.Type {
	case "auth_ok":
		log.Println("[spoke] authenticated successfully")

	case "auth_fail":
		log.Println("[spoke] authentication failed - bad token")

	case "sync":
		payloadData, _ := json.Marshal(msg.Payload)
		var sync SyncPayload
		json.Unmarshal(payloadData, &sync)
		n.store.ReplacePanes(sync.Panes)
		n.devicesMu.Lock()
		n.devices = make(map[string]Device)
		for _, d := range sync.Devices {
			n.devices[d.ID] = d
		}
		n.devicesMu.Unlock()
		log.Printf("[spoke] synced %d panes, %d devices", len(sync.Panes), len(sync.Devices))

	case "pane_update":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneUpdatePayload
		json.Unmarshal(payloadData, &payload)
		n.store.UpsertPane(payload.Pane)

	case "pane_delete":
		payloadData, _ := json.Marshal(msg.Payload)
		var payload PaneDeletePayload
		json.Unmarshal(payloadData, &payload)
		n.store.DeletePane(payload.PaneID)

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
	}
}

// SendToHub sends a message to the hub (spoke mode).
func (n *Node) SendToHub(msg WSMessage) error {
	n.hubConnMu.Lock()
	conn := n.hubConn
	n.hubConnMu.Unlock()
	if conn == nil {
		return nil
	}
	data, _ := json.Marshal(msg)
	return conn.WriteMessage(websocket.TextMessage, data)
}
