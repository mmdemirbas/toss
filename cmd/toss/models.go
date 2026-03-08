package main

import (
	"crypto/rand"
	"fmt"
	"time"
)

type Pane struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Content   string `json:"content"`
	Language  string `json:"language,omitempty"`
	Preview   *bool  `json:"preview,omitempty"`
	Order     int64  `json:"order,omitempty"`
	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Version   int64  `json:"version"`
}

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

type PaneUpdatePayload struct {
	Pane     Pane   `json:"pane"`
	SenderID string `json:"senderId"`
}

type PaneDeletePayload struct {
	PaneID   string `json:"paneId"`
	SenderID string `json:"senderId"`
}

type FileNotifyPayload struct {
	FileID   string `json:"fileId"`
	FileName string `json:"fileName"`
	SenderID string `json:"senderId"`
}

type SyncPayload struct {
	Panes   []Pane   `json:"panes"`
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

type ClipboardPayload struct {
	Content   string `json:"content,omitempty"`   // text clipboard
	ImageData string `json:"imageData,omitempty"` // base64-encoded image
	ImageExt  string `json:"imageExt,omitempty"`  // e.g. ".png"
	SenderID  string `json:"senderId"`
}

type ClipboardConfig struct {
	AutoTab     bool `json:"autoTab"`
	SyncEnabled bool `json:"syncEnabled"`
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}
