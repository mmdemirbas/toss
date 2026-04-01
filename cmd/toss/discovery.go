package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	DiscoveryPort = 7754
	MagicHeader   = "TOSS1"
)

type DiscoveryMsg struct {
	Magic    string `json:"m"`
	Type     string `json:"t"` // "seek", "hub", "hub_announce", "reverse_offer"
	DeviceID string `json:"d"`
	Port     int    `json:"p"`
	IP       string `json:"i,omitempty"`
	TargetID string `json:"x,omitempty"`
}

// DiscoverHub sends UDP broadcasts and waits for a hub response.
func DiscoverHub(deviceID string, timeout time.Duration) (string, string, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = conn.Close() }()

	msg := DiscoveryMsg{Magic: MagicHeader, Type: "seek", DeviceID: deviceID}
	data, _ := json.Marshal(msg)

	broadcastAll(conn, data)

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", "", fmt.Errorf("set read deadline: %w", err)
	}
	buf := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return "", "", nil // timeout
		}
		var resp DiscoveryMsg
		if err := json.Unmarshal(buf[:n], &resp); err != nil || resp.Magic != MagicHeader {
			continue
		}
		if resp.Type == "hub" && resp.DeviceID != deviceID {
			host, _, _ := net.SplitHostPort(addr.String())
			hubAddr := net.JoinHostPort(host, fmt.Sprintf("%d", resp.Port))
			return hubAddr, resp.DeviceID, nil
		}
	}
}

func broadcastAll(conn net.PacketConn, data []byte) {
	globalBcast := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
	if _, err := conn.WriteTo(data, globalBcast); err != nil {
		log.Printf("[discovery] broadcast to global: %v", err)
	}

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				bcast := broadcastAddr(ipnet)
				if _, err := conn.WriteTo(data, &net.UDPAddr{IP: bcast, Port: DiscoveryPort}); err != nil {
					log.Printf("[discovery] broadcast to %s: %v", bcast, err)
				}
			}
		}
	}
}

func openDiscoveryConn() (net.PacketConn, error) {
	var conn net.PacketConn
	var err error
	for attempts := range 5 {
		conn, err = net.ListenPacket("udp4", fmt.Sprintf(":%d", DiscoveryPort))
		if err == nil {
			return conn, nil
		}
		log.Printf("[discovery] port %d busy, retry %d/5...", DiscoveryPort, attempts+1)
		time.Sleep(2 * time.Second)
	}
	return nil, err
}

func handleDiscoveryMessage(conn net.PacketConn, remoteAddr net.Addr, msg DiscoveryMsg, deviceID string, httpPort int, collisionCh, reverseCh chan<- DiscoveryMsg) {
	switch msg.Type {
	case "seek":
		resp := DiscoveryMsg{Magic: MagicHeader, Type: "hub", DeviceID: deviceID, Port: httpPort}
		data, _ := json.Marshal(resp)
		if _, err := conn.WriteTo(data, remoteAddr); err != nil {
			log.Printf("[discovery] reply to seeker: %v", err)
		}
	case "hub_announce":
		host, _, _ := net.SplitHostPort(remoteAddr.String())
		msg.IP = host
		select {
		case collisionCh <- msg:
		default:
		}
	case "reverse_offer":
		if msg.TargetID != "" && msg.TargetID != deviceID {
			return
		}
		host, _, _ := net.SplitHostPort(remoteAddr.String())
		msg.IP = host
		select {
		case reverseCh <- msg:
		default:
		}
	}
}

// RunDiscoveryListener listens for broadcasts and responds as hub.
// Sends collision events when another hub is detected.
func RunDiscoveryListener(deviceID string, httpPort int, collisionCh chan<- DiscoveryMsg, reverseCh chan<- DiscoveryMsg, stopCh <-chan struct{}) {
	conn, err := openDiscoveryConn()
	if err != nil {
		log.Printf("[discovery] giving up on listener: %v", err)
		return
	}
	defer func() { _ = conn.Close() }()

	log.Printf("[discovery] listening on UDP :%d", DiscoveryPort)

	buf := make([]byte, 1024)
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		if err := conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			log.Printf("[discovery] set read deadline: %v", err)
		}
		n, remoteAddr, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		var msg DiscoveryMsg
		if err := json.Unmarshal(buf[:n], &msg); err != nil || msg.Magic != MagicHeader || msg.DeviceID == deviceID {
			continue
		}
		handleDiscoveryMessage(conn, remoteAddr, msg, deviceID, httpPort, collisionCh, reverseCh)
	}
}

func SendReverseOffer(deviceID string, httpPort int, targetHubID string) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	msg := DiscoveryMsg{
		Magic:    MagicHeader,
		Type:     "reverse_offer",
		DeviceID: deviceID,
		Port:     httpPort,
		TargetID: targetHubID,
	}
	data, _ := json.Marshal(msg)
	broadcastAll(conn, data)
}

// AnnounceHub periodically broadcasts hub presence for collision detection.
func AnnounceHub(deviceID string, httpPort int, stopCh <-chan struct{}) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	msg := DiscoveryMsg{
		Magic:    MagicHeader,
		Type:     "hub_announce",
		DeviceID: deviceID,
		Port:     httpPort,
	}
	data, _ := json.Marshal(msg)

	broadcastAll(conn, data)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			broadcastAll(conn, data)
		}
	}
}

func broadcastAddr(ipnet *net.IPNet) net.IP {
	ip := ipnet.IP.To4()
	mask := ipnet.Mask
	bcast := make(net.IP, 4)
	for i := range ip {
		bcast[i] = ip[i] | ^mask[i]
	}
	return bcast
}
