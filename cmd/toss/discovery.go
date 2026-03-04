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
	defer conn.Close()

	msg := DiscoveryMsg{Magic: MagicHeader, Type: "seek", DeviceID: deviceID}
	data, _ := json.Marshal(msg)

	broadcastAll(conn, data)

	conn.SetReadDeadline(time.Now().Add(timeout))
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
	conn.WriteTo(data, globalBcast)

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
				bcast := broadcastAddr(ipnet)
				conn.WriteTo(data, &net.UDPAddr{IP: bcast, Port: DiscoveryPort})
			}
		}
	}
}

// RunDiscoveryListener listens for broadcasts and responds as hub.
// Sends collision events when another hub is detected.
func RunDiscoveryListener(deviceID string, httpPort int, collisionCh chan<- DiscoveryMsg, reverseCh chan<- DiscoveryMsg, stopCh <-chan struct{}) {
	var conn net.PacketConn
	var err error
	for attempts := 0; attempts < 5; attempts++ {
		conn, err = net.ListenPacket("udp4", fmt.Sprintf(":%d", DiscoveryPort))
		if err == nil {
			break
		}
		log.Printf("[discovery] port %d busy, retry %d/5...", DiscoveryPort, attempts+1)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Printf("[discovery] giving up on listener: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[discovery] listening on UDP :%d", DiscoveryPort)

	buf := make([]byte, 1024)
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}
		var msg DiscoveryMsg
		if err := json.Unmarshal(buf[:n], &msg); err != nil || msg.Magic != MagicHeader || msg.DeviceID == deviceID {
			continue
		}

		switch msg.Type {
		case "seek":
			resp := DiscoveryMsg{
				Magic:    MagicHeader,
				Type:     "hub",
				DeviceID: deviceID,
				Port:     httpPort,
			}
			data, _ := json.Marshal(resp)
			conn.WriteTo(data, remoteAddr)

		case "hub_announce":
			host, _, _ := net.SplitHostPort(remoteAddr.String())
			msg.IP = host
			select {
			case collisionCh <- msg:
			default:
			}

		case "reverse_offer":
			if msg.TargetID != "" && msg.TargetID != deviceID {
				continue
			}
			host, _, _ := net.SplitHostPort(remoteAddr.String())
			msg.IP = host
			select {
			case reverseCh <- msg:
			default:
			}
		}
	}
}

func SendReverseOffer(deviceID string, httpPort int, targetHubID string) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	defer conn.Close()

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
	defer conn.Close()

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

func getLocalIP() string {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}
