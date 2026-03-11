package main

import (
	"crypto/rand"
	"fmt"
	"time"
)

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	JoinedAt int64  `json:"joinedAt"`
}

// WebSocket message envelope
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
}

// Specific payloads
type AuthPayload struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Port       int    `json:"port,omitempty"`
}

type FileNotifyPayload struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	SenderID string `json:"senderId"`
}

type SyncPayload struct {
	Devices []Device `json:"devices"`
}

type DevicesPayload struct {
	Devices []Device `json:"devices"`
}

// Hub collision: the other hub tells us to demote
type DemotePayload struct {
	HubDeviceID string `json:"hubDeviceId"`
	HubAddr     string `json:"hubAddr"`
	HubPort     int    `json:"hubPort"`
}

type ClipboardFileRef struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
}

type ClipboardPayload struct {
	Content   string             `json:"content,omitempty"`   // text clipboard
	ImageData string             `json:"imageData,omitempty"` // base64-encoded image
	ImageExt  string             `json:"imageExt,omitempty"`  // e.g. ".png"
	Files     []ClipboardFileRef `json:"files,omitempty"`     // copied files
	SenderID  string             `json:"senderId"`
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}
