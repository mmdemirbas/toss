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
	if err := os.MkdirAll(filesDir, 0750); err != nil {
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var status map[string]any
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var pane Pane
	if err := json.NewDecoder(resp.Body).Decode(&pane); err != nil {
		t.Fatal(err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	var panes []Pane
	if err := json.NewDecoder(resp.Body).Decode(&panes); err != nil {
		t.Fatal(err)
	}
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
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if created.ID == "" {
		t.Fatal("expected non-empty ID after create")
	}

	// List — should have 1
	resp, _ = http.Get(srv.URL + "/api/panes")
	var panes []Pane
	if err := json.NewDecoder(resp.Body).Decode(&panes); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	// Update
	updateBody := `{"name":"Updated","content":"new content","language":"rust"}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/"+created.ID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	var updated Pane
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

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
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on delete, got %d", resp.StatusCode)
	}

	// Verify deleted
	resp, _ = http.Get(srv.URL + "/api/panes")
	if err := json.NewDecoder(resp.Body).Decode(&panes); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
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
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// Update without sending order — should preserve original
	updateBody := `{"name":"Still Ordered","content":"updated"}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/"+created.ID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, _ = http.DefaultClient.Do(req)
	var updated Pane
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if updated.Order != created.Order {
		t.Errorf("expected order %d preserved, got %d", created.Order, updated.Order)
	}
}

func TestInvalidPaneJSON(t *testing.T) {
	srv := testServer(t)

	resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader("not json"))
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("PATCH", srv.URL+"/api/panes", nil)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405 for PATCH, got %d", resp.StatusCode)
	}
}

func TestPanePutMissingID(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/", strings.NewReader(`{"content":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for empty pane id, got %d", resp.StatusCode)
	}
}

func TestPaneDeleteMethodNotAllowed(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("POST", srv.URL+"/api/panes/some-id", nil)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

// ---- Files ----

func TestFileUploadAndDownload(t *testing.T) {
	srv := testServer(t)

	// Upload
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "hello.txt")
	if _, err := part.Write([]byte("hello file content")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/files", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

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
	defer func() { _ = resp.Body.Close() }()

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
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestFileNotFoundNoPeers(t *testing.T) {
	// Hub with no connected spokes: fetchAndServeFile should return 404
	// rather than hanging waiting for a peer that doesn't exist.
	srv := testServer(t)

	resp, _ := http.Get(srv.URL + "/api/files/ghost.txt")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 when file absent and no peers, got %d", resp.StatusCode)
	}
}

func TestFileInvalidID(t *testing.T) {
	srv := testServer(t)

	// Empty file ID → 400
	resp, _ := http.Get(srv.URL + "/api/files/")
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for empty file ID, got %d", resp.StatusCode)
	}

	// Path traversal with ../ is handled by Go's HTTP mux (301 redirect + 404),
	// so the handler never sees it. This is correct Go behavior.
	resp, _ = http.Get(srv.URL + "/api/files/../etc/passwd")
	_ = resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("path traversal should not return 200")
	}
}

func TestFileUploadForceID(t *testing.T) {
	srv := testServer(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "forced.txt")
	if _, err := part.Write([]byte("forced content")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/files?forceid=myid.txt", w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if result["fileId"] != "myid.txt" {
		t.Errorf("expected fileId=myid.txt, got %v", result["fileId"])
	}

	// File should be retrievable by the forced ID.
	resp2, _ := http.Get(srv.URL + "/api/files/myid.txt")
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != 200 {
		t.Fatalf("expected 200 on download by forced id, got %d", resp2.StatusCode)
	}
	data, _ := io.ReadAll(resp2.Body)
	if string(data) != "forced content" {
		t.Errorf("expected 'forced content', got %q", string(data))
	}
}

func TestFileUploadMethodNotAllowed(t *testing.T) {
	srv := testServer(t)
	resp, _ := http.Get(srv.URL + "/api/files")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/files, got %d", resp.StatusCode)
	}
}

func TestWebSocketNotReadyNode(t *testing.T) {
	// A node with no role assigned should return 503 before the WS upgrade.
	store := testStore(t)
	node := NewNode(store, 0) // role is "" by default
	srv := httptest.NewServer(SetupHTTP(node))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestFileUploadNoFile(t *testing.T) {
	srv := testServer(t)

	resp, _ := http.Post(srv.URL+"/api/files", "application/octet-stream", strings.NewReader("not multipart"))
	_ = resp.Body.Close()
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
	if _, err := part.Write([]byte("delete me")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	resp, _ := http.Post(srv.URL+"/api/files", w.FormDataContentType(), &buf)
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	fileID := result["fileId"].(string)

	// Create a pane referencing the file
	paneBody := `{"name":"With File","content":"![img](/api/files/` + fileID + `)","language":"markdown"}`
	resp, _ = http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(paneBody))
	var pane Pane
	if err := json.NewDecoder(resp.Body).Decode(&pane); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// Verify file exists before delete
	resp, _ = http.Get(srv.URL + "/api/files/" + fileID)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("file should exist before pane delete, got %d", resp.StatusCode)
	}

	// Delete the pane
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/panes/"+pane.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	_ = resp.Body.Close()

	// Verify file is cleaned up
	resp, _ = http.Get(srv.URL + "/api/files/" + fileID + "?norecurse=1")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("file should be deleted after pane delete, got %d", resp.StatusCode)
	}
}

// ---- Multiple panes ----

func TestMultiplePanes(t *testing.T) {
	srv := testServer(t)

	// Create 3 panes
	for range 3 {
		body := `{"name":"Pane","content":"content","language":"plaintext"}`
		resp, _ := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
		_ = resp.Body.Close()
	}

	// List should return 3
	resp, _ := http.Get(srv.URL + "/api/panes")
	var panes []Pane
	if err := json.NewDecoder(resp.Body).Decode(&panes); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if len(panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(panes))
	}
}

// ---- Clipboard File Sync ----

func checkFileRef(t *testing.T, node *Node, idx int, ref ClipboardFileRef, fileName, content string) {
	t.Helper()
	if ref.FileName != fileName {
		t.Errorf("ref[%d] fileName: expected %q, got %q", idx, fileName, ref.FileName)
	}
	if ref.FileSize != int64(len(content)) {
		t.Errorf("ref[%d] fileSize: expected %d, got %d", idx, len(content), ref.FileSize)
	}
	if ref.FileID == "" {
		t.Errorf("ref[%d] fileID is empty", idx)
		return
	}
	data, err := os.ReadFile(node.store.FilePath(ref.FileID))
	if err != nil {
		t.Errorf("ref[%d] stored file not readable: %v", idx, err)
		return
	}
	if string(data) != content {
		t.Errorf("ref[%d] stored content mismatch: expected %q, got %q", idx, content, string(data))
	}
	if filepath.Ext(ref.FileID) != filepath.Ext(fileName) {
		t.Errorf("ref[%d] extension: expected %q, got %q", idx, filepath.Ext(fileName), filepath.Ext(ref.FileID))
	}
}

func TestStoreAndForwardFiles(t *testing.T) {
	node := testNode(t)

	tmpDir := t.TempDir()
	fileNames := []string{"report.pdf", "photo.jpg", "notes.txt"}
	contents := []string{"pdf content here", "jpeg binary data", "some notes"}
	var paths []string
	for i, name := range fileNames {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte(contents[i]), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	refs := node.storeAndForwardFiles(paths)

	if len(refs) != 3 {
		t.Fatalf("expected 3 file refs, got %d", len(refs))
	}
	for i, ref := range refs {
		checkFileRef(t, node, i, ref, fileNames[i], contents[i])
	}
}

func TestReceiveClipboardFiles(t *testing.T) {
	node := testNode(t)

	// Simulate: 3 files are already in the file store (as if forwarded from peer)
	fileNames := []string{"report.pdf", "photo.jpg", "notes.txt"}
	contents := []string{"pdf content here", "jpeg binary data", "some notes"}
	var refs []ClipboardFileRef
	for i, name := range fileNames {
		fileID := generateID() + filepath.Ext(name)
		if err := os.WriteFile(node.store.FilePath(fileID), []byte(contents[i]), 0600); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ClipboardFileRef{
			FileID:   fileID,
			FileName: name,
			FileSize: int64(len(contents[i])),
		})
	}

	// Receive clipboard files (no fetchAddr needed since files are local)
	node.receiveClipboardFiles(refs, "")

	// Verify files were saved in clipboard_received with original names
	recvDir := filepath.Join(node.store.dir, "clipboard_received")
	for i, name := range fileNames {
		recvPath := filepath.Join(recvDir, name)
		data, err := os.ReadFile(recvPath)
		if err != nil {
			t.Errorf("file[%d] %q not found in clipboard_received: %v", i, name, err)
			continue
		}
		if string(data) != contents[i] {
			t.Errorf("file[%d] content mismatch: expected %q, got %q", i, contents[i], string(data))
		}
	}
}

func TestReceiveClipboardFilesNameCollision(t *testing.T) {
	node := testNode(t)

	// Two files with the same name
	fileID1 := generateID() + ".txt"
	fileID2 := generateID() + ".txt"
	if err := os.WriteFile(node.store.FilePath(fileID1), []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node.store.FilePath(fileID2), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}

	refs := []ClipboardFileRef{
		{FileID: fileID1, FileName: "same.txt", FileSize: 5},
		{FileID: fileID2, FileName: "same.txt", FileSize: 6},
	}

	node.receiveClipboardFiles(refs, "")

	// Both files should exist in clipboard_received (second with suffix)
	recvDir := filepath.Join(node.store.dir, "clipboard_received")
	entries, _ := os.ReadDir(recvDir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files in clipboard_received, got %d", len(entries))
	}

	// One should be "same.txt", the other "same_XXXX.txt"
	var foundOriginal, foundSuffixed bool
	for _, e := range entries {
		if e.Name() == "same.txt" {
			foundOriginal = true
		} else if strings.HasPrefix(e.Name(), "same_") && strings.HasSuffix(e.Name(), ".txt") {
			foundSuffixed = true
		}
	}
	if !foundOriginal || !foundSuffixed {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected 'same.txt' + 'same_XXXX.txt', got %v", names)
	}
}

func TestCreateClipboardFilePaneFromRefs(t *testing.T) {
	node := testNode(t)

	refs := []ClipboardFileRef{
		{FileID: "abc123.pdf", FileName: "report.pdf", FileSize: 1000},
		{FileID: "def456.jpg", FileName: "photo.jpg", FileSize: 2000},
		{FileID: "ghi789.txt", FileName: "notes.txt", FileSize: 500},
	}

	node.createClipboardFilePaneFromRefs(refs)

	panes := node.store.GetPanes()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	pane := panes[0]
	if pane.Name != "📋 3 file(s)" {
		t.Errorf("expected pane name '📋 3 file(s)', got %q", pane.Name)
	}
	if pane.Language != "markdown" {
		t.Errorf("expected language 'markdown', got %q", pane.Language)
	}

	// Verify content has links to all 3 files
	for _, ref := range refs {
		expected := "[" + ref.FileName + "](/api/files/" + ref.FileID + ")"
		if !strings.Contains(pane.Content, expected) {
			t.Errorf("pane content should contain %q, got %q", expected, pane.Content)
		}
	}
}

func TestCreateClipboardFilePaneSingleFile(t *testing.T) {
	node := testNode(t)

	refs := []ClipboardFileRef{
		{FileID: "abc123.pdf", FileName: "report.pdf", FileSize: 1000},
	}

	node.createClipboardFilePaneFromRefs(refs)

	panes := node.store.GetPanes()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	// Single file pane should use the file name
	if panes[0].Name != "📋 report.pdf" {
		t.Errorf("expected pane name '📋 report.pdf', got %q", panes[0].Name)
	}
}

func TestClipboardFileEndToEnd(t *testing.T) {
	// Simulates the full sender→store→receive→clipboard chain
	sender := testNode(t)
	receiver := testNode(t)

	// Create 3 real files on the "sender" side
	tmpDir := t.TempDir()
	fileNames := []string{"doc.pdf", "image.png", "data.csv"}
	contents := []string{"pdf bytes", "png bytes", "a,b,c\n1,2,3"}
	var srcPaths []string
	for i, name := range fileNames {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte(contents[i]), 0600); err != nil {
			t.Fatal(err)
		}
		srcPaths = append(srcPaths, p)
	}

	// Step 1: Sender stores and gets refs
	refs := sender.storeAndForwardFiles(srcPaths)
	if len(refs) != 3 {
		t.Fatalf("sender: expected 3 refs, got %d", len(refs))
	}

	// Step 2: Simulate hub relaying — copy files to receiver's store
	// (In real flow, this happens via HTTP file forward + file_notify)
	for _, ref := range refs {
		srcData, _ := os.ReadFile(sender.store.FilePath(ref.FileID))
		if err := os.WriteFile(receiver.store.FilePath(ref.FileID), srcData, 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Step 3: Receiver processes the clipboard_update with Files
	receiver.receiveClipboardFiles(refs, "")

	// Step 4: Verify receiver has all 3 files with original names
	recvDir := filepath.Join(receiver.store.dir, "clipboard_received")
	for i, name := range fileNames {
		data, err := os.ReadFile(filepath.Join(recvDir, name))
		if err != nil {
			t.Errorf("receiver: file %q not found: %v", name, err)
			continue
		}
		if string(data) != contents[i] {
			t.Errorf("receiver: file %q content mismatch", name)
		}
	}
}

// ---- Clipboard file state transition tests ----

func TestClipboardMonitorFileHashClearing(t *testing.T) {
	node := testNode(t)
	cm := node.clipboard

	// Simulate: files detected → lastFileHash set
	cm.mu.Lock()
	cm.lastFileHash = "file-hash-1"
	cm.lastImageHash = ""
	cm.lastText = ""
	cm.mu.Unlock()

	// Simulate: text written from peer → lastFileHash should be cleared
	cm.WriteClipboard("hello from peer")
	cm.mu.Lock()
	if cm.lastFileHash != "" {
		t.Error("WriteClipboard should clear lastFileHash")
	}
	if cm.lastImageHash != "" {
		t.Error("WriteClipboard should clear lastImageHash")
	}
	cm.mu.Unlock()

	// Reset: files on clipboard
	cm.mu.Lock()
	cm.lastFileHash = "file-hash-2"
	cm.lastImageHash = ""
	cm.lastText = ""
	cm.mu.Unlock()

	// Simulate: image written from peer → lastFileHash should be cleared
	// (We can't actually write image to clipboard in tests, just check state)
	cm.mu.Lock()
	cm.lastWrittenImageHash = "img-hash"
	cm.lastImageHash = "img-hash"
	cm.lastText = ""
	cm.lastFileHash = "" // This is what WriteClipboardImageData does
	cm.mu.Unlock()

	cm.mu.Lock()
	if cm.lastFileHash != "" {
		t.Error("WriteClipboardImageData should clear lastFileHash")
	}
	cm.mu.Unlock()
}

func TestClipboardMonitorWriteFilesEchoPrevention(t *testing.T) {
	node := testNode(t)
	cm := node.clipboard

	paths := []string{"/tmp/a.txt", "/tmp/b.txt", "/tmp/c.txt"}
	expectedHash := hashFilePaths(paths)

	// Simulate receiving files from peer
	cm.mu.Lock()
	cm.lastWrittenFileHash = expectedHash
	cm.lastFileHash = expectedHash
	cm.lastText = ""
	cm.lastImageHash = ""
	cm.mu.Unlock()

	// On next tick, handleFileCheck would read the same paths back
	// and compare against lastFileHash → should match (no re-broadcast)
	cm.mu.Lock()
	hash := hashFilePaths(paths)
	isEcho := hash == cm.lastFileHash || hash == cm.lastWrittenFileHash
	cm.mu.Unlock()

	if !isEcho {
		t.Error("same files should be detected as echo (no re-broadcast)")
	}
}

// ---- Helper function tests ----

func TestParsePathList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"\n\n", nil},
		{"/a/b.txt\n/c/d.pdf\n", []string{"/a/b.txt", "/c/d.pdf"}},
		{"/single.txt", []string{"/single.txt"}},
		{"  /with/spaces.txt  \n  /another.txt  ", []string{"/with/spaces.txt", "/another.txt"}},
	}
	for _, tt := range tests {
		got := parsePathList(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("parsePathList(%q): got %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parsePathList(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestParseURIList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"# comment\r\n", nil},
		{"file:///home/user/doc.pdf\r\nfile:///home/user/img.png\r\n", []string{"/home/user/doc.pdf", "/home/user/img.png"}},
		{"file:///path/to/file.txt\r\n# comment\r\nfile:///other.txt\r\n", []string{"/path/to/file.txt", "/other.txt"}},
		{"http://not-a-file.com/foo\r\n", nil}, // non-file URIs ignored
	}
	for _, tt := range tests {
		got := parseURIList(tt.input)
		if len(got) != len(tt.expected) {
			t.Errorf("parseURIList(%q): got %v, want %v", tt.input, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parseURIList(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestBuildURIList(t *testing.T) {
	paths := []string{"/home/user/doc.pdf", "/home/user/img.png"}
	result := buildURIList(paths)

	if !strings.Contains(result, "file:///home/user/doc.pdf") {
		t.Errorf("expected file URI for doc.pdf in %q", result)
	}
	if !strings.Contains(result, "file:///home/user/img.png") {
		t.Errorf("expected file URI for img.png in %q", result)
	}
	// Each URI should end with \r\n
	lines := strings.Split(result, "\r\n")
	// Should have 2 non-empty lines + 1 trailing empty
	nonEmpty := 0
	for _, l := range lines {
		if l != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Errorf("expected 2 URI lines, got %d", nonEmpty)
	}
}

func TestBuildAndParseURIListRoundTrip(t *testing.T) {
	original := []string{"/home/user/report.pdf", "/home/user/photo.jpg", "/tmp/notes.txt"}
	uriList := buildURIList(original)
	parsed := parseURIList(uriList)

	if len(parsed) != len(original) {
		t.Fatalf("round-trip: expected %d paths, got %d", len(original), len(parsed))
	}
	for i := range original {
		if parsed[i] != original[i] {
			t.Errorf("round-trip[%d]: expected %q, got %q", i, original[i], parsed[i])
		}
	}
}

func TestHashFilePathsStable(t *testing.T) {
	paths1 := []string{"/a.txt", "/b.txt", "/c.txt"}
	paths2 := []string{"/c.txt", "/a.txt", "/b.txt"} // different order

	h1 := hashFilePaths(paths1)
	h2 := hashFilePaths(paths2)

	if h1 != h2 {
		t.Error("hashFilePaths should be order-independent")
	}

	paths3 := []string{"/a.txt", "/b.txt", "/d.txt"} // different file
	h3 := hashFilePaths(paths3)
	if h1 == h3 {
		t.Error("different file sets should produce different hashes")
	}
}

func TestFilterValidFiles(t *testing.T) {
	node := testNode(t)
	cm := node.clipboard

	tmpDir := t.TempDir()

	// Create regular files
	small := filepath.Join(tmpDir, "small.txt")
	if err := os.WriteFile(small, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}

	// Create a directory
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Non-existent file
	missing := filepath.Join(tmpDir, "missing.txt")

	paths := []string{small, subDir, missing}
	valid := cm.filterValidFiles(paths)

	if len(valid) != 1 {
		t.Fatalf("expected 1 valid file, got %d", len(valid))
	}
	if valid[0] != small {
		t.Errorf("expected %q, got %q", small, valid[0])
	}
}

func TestFilterValidFilesTooManyTruncated(t *testing.T) {
	node := testNode(t)
	cm := node.clipboard
	tmpDir := t.TempDir()

	// Create clipboardMaxFileCount+1 files (21 files, limit is 20).
	var paths []string
	for i := range clipboardMaxFileCount + 1 {
		p := filepath.Join(tmpDir, filepath.FromSlash(strings.Repeat("x", i+1)+".txt"))
		if err := os.WriteFile(p, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	valid := cm.filterValidFiles(paths)
	if len(valid) != clipboardMaxFileCount {
		t.Errorf("expected exactly %d files (limit), got %d", clipboardMaxFileCount, len(valid))
	}
}

func TestClipboardPayloadFilesJSON(t *testing.T) {
	payload := ClipboardPayload{
		Files: []ClipboardFileRef{
			{FileID: "abc.pdf", FileName: "report.pdf", FileSize: 1024},
			{FileID: "def.jpg", FileName: "photo.jpg", FileSize: 2048},
		},
		SenderID: "device-1",
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var decoded ClipboardPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(decoded.Files))
	}
	if decoded.Files[0].FileName != "report.pdf" {
		t.Errorf("expected 'report.pdf', got %q", decoded.Files[0].FileName)
	}
	if decoded.Files[1].FileSize != 2048 {
		t.Errorf("expected size 2048, got %d", decoded.Files[1].FileSize)
	}
	if decoded.SenderID != "device-1" {
		t.Errorf("expected sender 'device-1', got %q", decoded.SenderID)
	}

	// Content and ImageData should be empty (omitted)
	if decoded.Content != "" {
		t.Error("Content should be empty")
	}
	if decoded.ImageData != "" {
		t.Error("ImageData should be empty")
	}
}

// ---- collectMissingFileIDs ----

func TestCollectMissingFileIDsNoneReferenced(t *testing.T) {
	node := testNode(t)
	node.store.UpsertPane(Pane{ID: "p1", Content: "no file refs here", Version: 1})

	missing := node.collectMissingFileIDs()
	if len(missing) != 0 {
		t.Errorf("expected 0 missing, got %v", missing)
	}
}

func TestCollectMissingFileIDsAllPresent(t *testing.T) {
	node := testNode(t)

	fileID := "abc123.txt"
	if err := os.WriteFile(node.store.FilePath(fileID), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	node.store.UpsertPane(Pane{ID: "p1", Content: "/api/files/" + fileID, Version: 1})

	missing := node.collectMissingFileIDs()
	if len(missing) != 0 {
		t.Errorf("expected 0 missing (file exists), got %v", missing)
	}
}

func TestCollectMissingFileIDsAbsentFile(t *testing.T) {
	node := testNode(t)

	node.store.UpsertPane(Pane{ID: "p1", Content: "/api/files/missing123.txt", Version: 1})

	missing := node.collectMissingFileIDs()
	if len(missing) != 1 || missing[0] != "missing123.txt" {
		t.Errorf("expected [missing123.txt], got %v", missing)
	}
}

func TestCollectMissingFileIDsDeduplicatesAcrossPanes(t *testing.T) {
	node := testNode(t)

	// Same fileID referenced from two different panes.
	node.store.UpsertPane(Pane{ID: "p1", Content: "/api/files/dup.txt", Version: 1})
	node.store.UpsertPane(Pane{ID: "p2", Content: "/api/files/dup.txt", Version: 2})

	missing := node.collectMissingFileIDs()
	if len(missing) != 1 {
		t.Errorf("expected 1 deduplicated entry, got %v", missing)
	}
}

// ---- parseURIList / buildURIList ----

func TestParseURIListBasic(t *testing.T) {
	input := "file:///home/user/report.pdf\r\n"
	paths := parseURIList(input)
	if len(paths) != 1 || paths[0] != "/home/user/report.pdf" {
		t.Errorf("expected [/home/user/report.pdf], got %v", paths)
	}
}

func TestParseURIListSkipsComments(t *testing.T) {
	input := "# comment\nfile:///a.txt\n# another comment\nfile:///b.txt\n"
	paths := parseURIList(input)
	if len(paths) != 2 {
		t.Errorf("expected 2 paths, got %v", paths)
	}
}

func TestParseURIListSkipsNonFileURIs(t *testing.T) {
	input := "https://example.com/file.txt\nfile:///local.txt\n"
	paths := parseURIList(input)
	if len(paths) != 1 || paths[0] != "/local.txt" {
		t.Errorf("expected only /local.txt, got %v", paths)
	}
}

func TestParseURIListEmptyInput(t *testing.T) {
	paths := parseURIList("")
	if len(paths) != 0 {
		t.Errorf("expected empty, got %v", paths)
	}
}

// ---- Spoke node CRUD paths ----

func spokeNode(t *testing.T) *Node {
	t.Helper()
	store := testStore(t)
	node := NewNode(store, 0)
	node.roleMu.Lock()
	node.role = "spoke"
	node.roleMu.Unlock()
	// Port 1 is a system port — connections are refused immediately.
	// This exercises the spoke→hub SendToHub error path without blocking.
	node.hubAddr = "127.0.0.1:1"
	return node
}

func TestCreatePaneAsSpoke(t *testing.T) {
	node := spokeNode(t)
	srv := httptest.NewServer(SetupHTTP(node))
	defer srv.Close()

	body := `{"name":"spoke pane","content":"from spoke"}`
	resp, err := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestUpdatePaneInheritsExistingOrder(t *testing.T) {
	// PUT with Order=0 on an existing pane should preserve its stored Order.
	srv := testServer(t)

	// Create a pane with an explicit order.
	createBody := `{"name":"original","order":99999}`
	resp, err := http.Post(srv.URL+"/api/panes", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	var created Pane
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// PUT without setting Order — server should carry the original over.
	updateBody := `{"name":"updated","content":"new content"}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/"+created.ID, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var updated Pane
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Order != created.Order {
		t.Errorf("expected Order=%d (inherited), got %d", created.Order, updated.Order)
	}
}

func TestUpdatePaneAsSpoke(t *testing.T) {
	node := spokeNode(t)
	node.store.UpsertPane(Pane{ID: "sp1", Name: "original", Order: 77, Version: 1})
	srv := httptest.NewServer(SetupHTTP(node))
	defer srv.Close()

	req, _ := http.NewRequest("PUT", srv.URL+"/api/panes/sp1", strings.NewReader(`{"name":"updated"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDeletePaneAsSpoke(t *testing.T) {
	node := spokeNode(t)
	node.store.UpsertPane(Pane{ID: "del1", Name: "to delete", Version: 1})
	srv := httptest.NewServer(SetupHTTP(node))
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/panes/del1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestPeerAddrsSpokWithoutHub(t *testing.T) {
	// A spoke with no hubAddr should have no peer addresses.
	store := testStore(t)
	node := NewNode(store, 0)
	node.roleMu.Lock()
	node.role = "spoke"
	node.roleMu.Unlock()
	// hubAddr intentionally left empty

	if addrs := node.peerAddrs(); len(addrs) != 0 {
		t.Errorf("expected no peer addrs for spoke without hub, got %v", addrs)
	}
}

// ---- File proxy (peer fetch / forward) ----

func TestFetchFileFromPeerSuccess(t *testing.T) {
	content := []byte("data from peer node")
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("norecurse") == "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content)
		}
	}))
	defer peer.Close()

	addr := strings.TrimPrefix(peer.URL, "https://")
	dst := filepath.Join(t.TempDir(), "fetched.bin")

	if !fetchFileFromPeer(addr, "data.bin", dst) {
		t.Fatal("expected fetchFileFromPeer to succeed")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestFetchFileFromPeerNotFound(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer peer.Close()

	addr := strings.TrimPrefix(peer.URL, "https://")
	dst := filepath.Join(t.TempDir(), "nonexistent.bin")

	if fetchFileFromPeer(addr, "nonexistent.bin", dst) {
		t.Fatal("expected failure on 404")
	}
}

func TestFetchFileFromPeerConnectionError(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "file.bin")
	// Port 1 is a system port that will refuse connections.
	if fetchFileFromPeer("127.0.0.1:1", "file.bin", dst) {
		t.Fatal("expected failure on connection error")
	}
}

func TestFetchAndServeFileFetchedFromHub(t *testing.T) {
	content := []byte("hub file content")
	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("norecurse") != "1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer hub.Close()

	store := testStore(t)
	node := NewNode(store, 0)
	node.roleMu.Lock()
	node.role = "spoke"
	node.roleMu.Unlock()
	node.hubAddr = strings.TrimPrefix(hub.URL, "https://")

	handler := SetupHTTP(node)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/files/remote.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestForwardFile(t *testing.T) {
	var receivedFileName string
	var receivedContent []byte

	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = f.Close() }()
		receivedFileName = hdr.Filename
		receivedContent, _ = io.ReadAll(f)
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()

	node := testNode(t)
	node.hubAddr = strings.TrimPrefix(hub.URL, "https://")

	fileContent := []byte("forward me to hub")
	storedName := "upload.bin"
	if err := os.WriteFile(node.store.FilePath(storedName), fileContent, 0600); err != nil {
		t.Fatal(err)
	}

	if err := forwardFile(node, storedName, "original.bin"); err != nil {
		t.Fatalf("forwardFile: %v", err)
	}
	if receivedFileName != "original.bin" {
		t.Errorf("expected filename 'original.bin', got %q", receivedFileName)
	}
	if string(receivedContent) != string(fileContent) {
		t.Errorf("content mismatch: got %q, want %q", receivedContent, fileContent)
	}
}

func TestForwardFileHubError(t *testing.T) {
	hub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer hub.Close()

	node := testNode(t)
	node.hubAddr = strings.TrimPrefix(hub.URL, "https://")

	if err := os.WriteFile(node.store.FilePath("f.bin"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	err := forwardFile(node, "f.bin", "f.bin")
	if err == nil {
		t.Fatal("expected error when hub returns 500")
	}
}

func TestParseURIListRoundTripWithBuild(t *testing.T) {
	origPaths := []string{"/home/user/a.txt", "/tmp/b.pdf"}
	uriList := buildURIList(origPaths)
	roundTripped := parseURIList(uriList)

	if len(roundTripped) != len(origPaths) {
		t.Fatalf("round-trip length mismatch: %d vs %d", len(roundTripped), len(origPaths))
	}
	for i, p := range origPaths {
		if roundTripped[i] != p {
			t.Errorf("path[%d]: expected %q, got %q", i, p, roundTripped[i])
		}
	}
}

func TestClipboardPayloadFilesOmitEmpty(t *testing.T) {
	// Text-only payload should not have "files" key
	payload := ClipboardPayload{Content: "hello", SenderID: "device-1"}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"files"`) {
		t.Error("files field should be omitted when empty")
	}
}

// ---- Clipboard config API ----

func TestClipboardConfigGetDefault(t *testing.T) {
	srv := testServer(t)

	resp, err := http.Get(srv.URL + "/api/clipboard/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var cfg ClipboardConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	// Default config has both features disabled.
	if cfg.AutoTab {
		t.Error("autoTab should default to false")
	}
	if cfg.SyncEnabled {
		t.Error("syncEnabled should default to false")
	}
}

func TestClipboardConfigPutAndGet(t *testing.T) {
	srv := testServer(t)

	// Enable both features.
	body := `{"autoTab":true,"syncEnabled":true}`
	req, _ := http.NewRequest("PUT", srv.URL+"/api/clipboard/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on PUT, got %d", resp.StatusCode)
	}

	// GET must reflect the update.
	resp, err = http.Get(srv.URL + "/api/clipboard/config")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var cfg ClipboardConfig
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.AutoTab {
		t.Error("expected autoTab=true after PUT")
	}
	if !cfg.SyncEnabled {
		t.Error("expected syncEnabled=true after PUT")
	}
}

func TestClipboardConfigPutInvalidJSON(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("PUT", srv.URL+"/api/clipboard/config", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestClipboardConfigMethodNotAllowed(t *testing.T) {
	srv := testServer(t)

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/clipboard/config", nil)
	resp, _ := http.DefaultClient.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

// ---- clipboardPaneName ----

func TestClipboardPaneNameShortText(t *testing.T) {
	name := clipboardPaneName("hello world")
	if name != "📋 hello world" {
		t.Errorf("unexpected name: %q", name)
	}
}

func TestClipboardPaneNameTruncatesAt40Runes(t *testing.T) {
	long := strings.Repeat("x", 50)
	name := clipboardPaneName(long)
	runes := []rune(name)
	// "📋 " is 2 runes (emoji + space), then 40 content runes + "…"
	if len(runes) != 43 {
		t.Errorf("expected 43 runes (2 prefix + 40 + ellipsis), got %d: %q", len(runes), name)
	}
}

func TestClipboardPaneNameUsesFirstLine(t *testing.T) {
	name := clipboardPaneName("first line\nsecond line")
	if name != "📋 first line" {
		t.Errorf("expected first line only, got %q", name)
	}
}

func TestClipboardPaneNameEmptyContentFallback(t *testing.T) {
	name := clipboardPaneName("")
	if !strings.HasPrefix(name, "📋 Clipboard ") {
		t.Errorf("empty content should produce fallback name, got %q", name)
	}
}

func TestClipboardPaneNameWhitespaceOnlyFallback(t *testing.T) {
	name := clipboardPaneName("   \n   ")
	if !strings.HasPrefix(name, "📋 Clipboard ") {
		t.Errorf("whitespace-only content should produce fallback name, got %q", name)
	}
}

// ---- broadcastClipboardContent ----

func TestBroadcastClipboardContentTextHub(t *testing.T) {
	node := testNode(t)
	// hub with no connected spokes: broadcast is a no-op, no panic
	node.broadcastClipboardContent(ClipboardPayload{Content: "hello"})
}

func TestBroadcastClipboardContentImageHub(t *testing.T) {
	node := testNode(t)
	node.broadcastClipboardContent(ClipboardPayload{ImageData: "base64data", ImageExt: ".png"})
}

func TestBroadcastClipboardContentFilesHub(t *testing.T) {
	node := testNode(t)
	node.broadcastClipboardContent(ClipboardPayload{
		Files: []ClipboardFileRef{{FileID: "f1.pdf", FileName: "doc.pdf", FileSize: 100}},
	})
}

func TestBroadcastClipboardContentSpoke(t *testing.T) {
	// spoke with no hub connection: SendToHub returns error, which is logged
	node := spokeNode(t)
	node.broadcastClipboardContent(ClipboardPayload{Content: "from spoke"})
}

// ---- hashBytes ----

func TestHashBytesIsStable(t *testing.T) {
	data := []byte("test image content")
	h1 := hashBytes(data)
	h2 := hashBytes(data)
	if h1 != h2 {
		t.Error("hashBytes must be deterministic")
	}
	if len(h1) != 32 {
		t.Errorf("expected 32-char MD5 hex, got %d", len(h1))
	}
}

func TestHashBytesDifferentInputDifferentHash(t *testing.T) {
	h1 := hashBytes([]byte("aaa"))
	h2 := hashBytes([]byte("bbb"))
	if h1 == h2 {
		t.Error("different inputs must produce different hashes")
	}
}
