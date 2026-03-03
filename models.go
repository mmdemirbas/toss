package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// Block types
const (
	BlockText     = "text"
	BlockCode     = "code"
	BlockMarkdown = "markdown"
	BlockImage    = "image"
	BlockFile     = "file"
)

type Block struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Content  string `json:"content,omitempty"`
	Language string `json:"language,omitempty"`
	FileID   string `json:"fileId,omitempty"`
	FileName string `json:"fileName,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	FileSize int64  `json:"fileSize,omitempty"`
}

type Pane struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Blocks    []Block `json:"blocks"`
	CreatedBy string  `json:"createdBy"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Version   int64   `json:"version"`
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
	Token      string `json:"token"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
}

type PaneUpdatePayload struct {
	Pane     Pane   `json:"pane"`
	SenderID string `json:"senderId"`
}

type PaneDeletePayload struct {
	PaneID   string `json:"paneId"`
	SenderID string `json:"senderId"`
}

type SyncPayload struct {
	Panes   []Pane   `json:"panes"`
	Devices []Device `json:"devices"`
}

type DevicesPayload struct {
	Devices []Device `json:"devices"`
}

// Helper to generate IDs
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func generateToken() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous chars
	result := make([]byte, 6)
	for i := range result {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		result[i] = chars[n.Int64()]
	}
	return string(result)
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}
