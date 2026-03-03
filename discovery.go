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
	MagicHeader   = "LANPANE"
)

type DiscoveryMsg struct {
	Magic    string `json:"magic"`
	Type     string `json:"type"` // "discover" or "hub"
	DeviceID string `json:"deviceId"`
	Port     int    `json:"port"`
	Name     string `json:"name"`
}

// DiscoverHub sends UDP broadcasts and waits for a hub response.
// Returns hub address (ip:port) or empty string if no hub found.
func DiscoverHub(deviceID string, timeout time.Duration) (string, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", fmt.Errorf("listen udp: %w", err)
	}
	defer conn.Close()

	msg := DiscoveryMsg{
		Magic:    MagicHeader,
		Type:     "discover",
		DeviceID: deviceID,
	}
	data, _ := json.Marshal(msg)

	broadcast := &net.UDPAddr{IP: net.IPv4bcast, Port: DiscoveryPort}
	_, err = conn.WriteTo(data, broadcast)
	if err != nil {
		// Try all interfaces if broadcast fails
		ifaces, _ := net.Interfaces()
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
					bcast := broadcastAddr(ipnet)
					conn.WriteTo(data, &net.UDPAddr{IP: bcast, Port: DiscoveryPort})
				}
			}
		}
	}

	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1024)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return "", nil // timeout, no hub found
		}
		var resp DiscoveryMsg
		if err := json.Unmarshal(buf[:n], &resp); err != nil {
			continue
		}
		if resp.Magic == MagicHeader && resp.Type == "hub" && resp.DeviceID != deviceID {
			// Extract the source IP from the response
			return fmt.Sprintf("%s:%d", resp.Name, resp.Port), nil
		}
	}
}

// RunDiscoveryListener listens for discovery broadcasts and responds as hub.
func RunDiscoveryListener(deviceID string, httpPort int, stopCh <-chan struct{}) {
	addr := &net.UDPAddr{Port: DiscoveryPort}
	conn, err := net.ListenPacket("udp4", addr.String())
	if err != nil {
		log.Printf("[discovery] failed to listen on port %d: %v", DiscoveryPort, err)
		return
	}
	defer conn.Close()

	log.Printf("[discovery] listening for broadcasts on :%d", DiscoveryPort)

	// Get local IPs for response
	localIP := getLocalIP()

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
		if err := json.Unmarshal(buf[:n], &msg); err != nil || msg.Magic != MagicHeader {
			continue
		}
		if msg.Type == "discover" && msg.DeviceID != deviceID {
			resp := DiscoveryMsg{
				Magic:    MagicHeader,
				Type:     "hub",
				DeviceID: deviceID,
				Port:     httpPort,
				Name:     localIP,
			}
			data, _ := json.Marshal(resp)
			conn.WriteTo(data, remoteAddr)
			log.Printf("[discovery] responded to discovery from %s", remoteAddr)
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
