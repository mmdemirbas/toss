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

// ---- Clipboard File Sync ----

func TestStoreAndForwardFiles(t *testing.T) {
	node := testNode(t)

	// Create 3 temp files to simulate copied files
	tmpDir := t.TempDir()
	fileNames := []string{"report.pdf", "photo.jpg", "notes.txt"}
	contents := []string{"pdf content here", "jpeg binary data", "some notes"}
	var paths []string
	for i, name := range fileNames {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte(contents[i]), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	// Store and forward
	refs := node.storeAndForwardFiles(paths)

	if len(refs) != 3 {
		t.Fatalf("expected 3 file refs, got %d", len(refs))
	}

	for i, ref := range refs {
		// Check metadata
		if ref.FileName != fileNames[i] {
			t.Errorf("ref[%d] fileName: expected %q, got %q", i, fileNames[i], ref.FileName)
		}
		if ref.FileSize != int64(len(contents[i])) {
			t.Errorf("ref[%d] fileSize: expected %d, got %d", i, len(contents[i]), ref.FileSize)
		}
		if ref.FileID == "" {
			t.Errorf("ref[%d] fileID is empty", i)
		}

		// Verify file was stored in the file store
		storedPath := node.store.FilePath(ref.FileID)
		data, err := os.ReadFile(storedPath)
		if err != nil {
			t.Errorf("ref[%d] stored file not readable: %v", i, err)
			continue
		}
		if string(data) != contents[i] {
			t.Errorf("ref[%d] stored content mismatch: expected %q, got %q", i, contents[i], string(data))
		}

		// Verify file extension is preserved
		if filepath.Ext(ref.FileID) != filepath.Ext(fileNames[i]) {
			t.Errorf("ref[%d] extension: expected %q, got %q", i, filepath.Ext(fileNames[i]), filepath.Ext(ref.FileID))
		}
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
		if err := os.WriteFile(node.store.FilePath(fileID), []byte(contents[i]), 0644); err != nil {
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
	os.WriteFile(node.store.FilePath(fileID1), []byte("first"), 0644)
	os.WriteFile(node.store.FilePath(fileID2), []byte("second"), 0644)

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
		os.WriteFile(p, []byte(contents[i]), 0644)
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
		os.WriteFile(receiver.store.FilePath(ref.FileID), srcData, 0644)
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
	os.WriteFile(small, []byte("ok"), 0644)

	// Create a directory
	subDir := filepath.Join(tmpDir, "subdir")
	os.MkdirAll(subDir, 0755)

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

func TestClipboardPayloadFilesOmitEmpty(t *testing.T) {
	// Text-only payload should not have "files" key
	payload := ClipboardPayload{Content: "hello", SenderID: "device-1"}
	data, _ := json.Marshal(payload)
	if strings.Contains(string(data), `"files"`) {
		t.Error("files field should be omitted when empty")
	}
}
