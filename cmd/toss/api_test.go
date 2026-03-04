package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- Test helpers ----

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		t.Fatal(err)
	}
	return &Store{
		dir:      dir,
		panes:    make(map[string]*Pane),
		filesDir: filesDir,
		config: Config{
			DeviceID:   "test-device-001",
			DeviceName: "test-device",
		},
	}
}

func testNode(t *testing.T) *Node {
	t.Helper()
	store := testStore(t)
	node := NewNode(store, 0)
	node.roleMu.Lock()
	node.role = "hub"
	node.roleMu.Unlock()
	node.devicesMu.Lock()
	node.devices[store.config.DeviceID] = Device{
		ID: store.config.DeviceID, Name: store.config.DeviceName, Role: "hub",
	}
	node.devicesMu.Unlock()
	return node
}

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	node := testNode(t)
	handler := SetupHTTP(node)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// ---- Status ----

func TestGetStatus(t *testing.T) {
	srv := testServer(t)

	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["role"] != "hub" {
		t.Errorf("expected role=hub, got %v", status["role"])
	}
	if status["deviceId"] != "test-device-001" {
		t.Errorf("expected deviceId=test-device-001, got %v", status["deviceId"])
	}
	if status["deviceName"] != "test-device" {
		t.Errorf("expected deviceName=test-device, got %v", status["deviceName"])
	}
}

// ---- Panes CRUD ----

func TestCreatePane(t *testing.T) {
	srv := testServer(t)

	body := `{"name":"Test Pane","content":"Hello world","language":"plaintext"}`
	resp, err := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var pane Pane
	json.NewDecoder(resp.Body).Decode(&pane)

	if pane.ID == "" {
		t.Error("expected non-empty ID")
	}
	if pane.Name != "Test Pane" {
		t.Errorf("expected name 'Test Pane', got %q", pane.Name)
	}
	if pane.Content != "Hello world" {
		t.Errorf("expected content 'Hello world', got %q", pane.Content)
	}
	if pane.Language != "plaintext" {
		t.Errorf("expected language 'plaintext', got %q", pane.Language)
	}
	if pane.CreatedAt == 0 {
		t.Error("expected non-zero CreatedAt")
	}
	if pane.Version == 0 {
		t.Error("expected non-zero Version")
	}
}

func TestListPanesEmpty(t *testing.T) {
	srv := testServer(t)

	resp, err := http.Get(srv.URL + "/api/panes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var panes []Pane
	json.NewDecoder(resp.Body).Decode(&panes)
	if len(panes) != 0 {
		t.Fatalf("expected 0 panes, got %d", len(panes))
	}
}

func TestPanesCRUD(t *testing.T) {
	srv := testServer(t)

	// Create
	body := `{"name":"CRUD Test","content":"original","language":"go"}`
	resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
	var created Pane
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	if created.ID == "" {
		t.Fatal("expected non-empty ID after create")
	}

	// List — should have 1
	resp, _ = http.Get(srv.URL + "/api/panes")
	var panes []Pane
	json.NewDecoder(resp.Body).Decode(&panes)
	resp.Body.Close()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	// Update
	updateBody := `{"name":"Updated","content":"new content","language":"rust"}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/"+created.ID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	var updated Pane
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()

	if updated.Name != "Updated" {
		t.Errorf("expected name 'Updated', got %q", updated.Name)
	}
	if updated.Content != "new content" {
		t.Errorf("expected content 'new content', got %q", updated.Content)
	}
	if updated.Language != "rust" {
		t.Errorf("expected language 'rust', got %q", updated.Language)
	}

	// Delete
	req, _ = http.NewRequest("DELETE", srv.URL+"/api/panes/"+created.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on delete, got %d", resp.StatusCode)
	}

	// Verify deleted
	resp, _ = http.Get(srv.URL + "/api/panes")
	json.NewDecoder(resp.Body).Decode(&panes)
	resp.Body.Close()
	if len(panes) != 0 {
		t.Fatalf("expected 0 panes after delete, got %d", len(panes))
	}
}

func TestUpdatePreservesOrder(t *testing.T) {
	srv := testServer(t)

	// Create with explicit order
	body := `{"name":"Ordered","content":"test","language":"go","order":12345}`
	resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
	var created Pane
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Update without sending order — should preserve original
	updateBody := `{"name":"Still Ordered","content":"updated"}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/"+created.ID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	var updated Pane
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()

	if updated.Order != created.Order {
		t.Errorf("expected order %d preserved, got %d", created.Order, updated.Order)
	}
}

func TestInvalidPaneJSON(t *testing.T) {
	srv := testServer(t)

	resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader("not json"))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("PATCH", srv.URL+"/api/panes", nil)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405 for PATCH, got %d", resp.StatusCode)
	}
}

// ---- Files ----

func TestFileUploadAndDownload(t *testing.T) {
	srv := testServer(t)

	// Upload
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "hello.txt")
	part.Write([]byte("hello file content"))
	w.Close()

	resp, err := http.Post(srv.URL+"/api/files", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on upload, got %d", resp.StatusCode)
	}
	fileID, ok := result["fileId"].(string)
	if !ok || fileID == "" {
		t.Fatal("expected non-empty fileId")
	}
	if result["fileName"] != "hello.txt" {
		t.Errorf("expected fileName 'hello.txt', got %v", result["fileName"])
	}

	// Download
	resp, err = http.Get(srv.URL + "/api/files/" + fileID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on download, got %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "hello file content" {
		t.Errorf("expected 'hello file content', got %q", string(data))
	}
}

func TestFileNotFound(t *testing.T) {
	srv := testServer(t)

	resp, _ := http.Get(srv.URL + "/api/files/nonexistent.txt?norecurse=1")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestFileInvalidID(t *testing.T) {
	srv := testServer(t)

	// Empty file ID → 400
	resp, _ := http.Get(srv.URL + "/api/files/")
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for empty file ID, got %d", resp.StatusCode)
	}

	// Path traversal with ../ is handled by Go's HTTP mux (301 redirect + 404),
	// so the handler never sees it. This is correct Go behavior.
	resp, _ = http.Get(srv.URL + "/api/files/../etc/passwd")
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("path traversal should not return 200")
	}
}

func TestFileUploadNoFile(t *testing.T) {
	srv := testServer(t)

	resp, _ := http.Post(srv.URL+"/api/files", "application/octet-stream", strings.NewReader("not multipart"))
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for missing file, got %d", resp.StatusCode)
	}
}

// ---- Pane delete cleans up files ----

func TestDeletePaneCleansUpFiles(t *testing.T) {
	srv := testServer(t)

	// Upload a file
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "cleanup.txt")
	part.Write([]byte("delete me"))
	w.Close()

	resp, _ := http.Post(srv.URL+"/api/files", w.FormDataContentType(), &buf)
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	fileID := result["fileId"].(string)

	// Create a pane referencing the file
	paneBody := `{"name":"With File","content":"![img](/api/files/` + fileID + `)","language":"markdown"}`
	resp, _ = http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(paneBody))
	var pane Pane
	json.NewDecoder(resp.Body).Decode(&pane)
	resp.Body.Close()

	// Verify file exists before delete
	resp, _ = http.Get(srv.URL + "/api/files/" + fileID)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("file should exist before pane delete, got %d", resp.StatusCode)
	}

	// Delete the pane
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/panes/"+pane.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()

	// Verify file is cleaned up
	resp, _ = http.Get(srv.URL + "/api/files/" + fileID + "?norecurse=1")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("file should be deleted after pane delete, got %d", resp.StatusCode)
	}
}

// ---- Multiple panes ----

func TestMultiplePanes(t *testing.T) {
	srv := testServer(t)

	// Create 3 panes
	for i := 0; i < 3; i++ {
		body := `{"name":"Pane","content":"content","language":"plaintext"}`
		resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}

	// List should return 3
	resp, _ := http.Get(srv.URL + "/api/panes")
	var panes []Pane
	json.NewDecoder(resp.Body).Decode(&panes)
	resp.Body.Close()

	if len(panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(panes))
	}
}
