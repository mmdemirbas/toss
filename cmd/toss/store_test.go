package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- UpsertPane version conflict ----

func TestUpsertPaneNewPane(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "p1", Content: "hello", Type: "code", Version: 100})

	got := store.GetPane("p1")
	if got == nil {
		t.Fatal("pane not stored")
	}
	if got.Content != "hello" {
		t.Errorf("expected 'hello', got %q", got.Content)
	}
}

func TestUpsertPaneLowerVersionDropped(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "p1", Content: "original", Type: "code", Version: 1000})
	store.UpsertPane(Pane{ID: "p1", Content: "stale", Type: "code", Version: 999})

	got := store.GetPane("p1")
	if got.Content != "original" || got.Version != 1000 {
		t.Errorf("lower version should be dropped; got content=%q version=%d", got.Content, got.Version)
	}
}

func TestUpsertPaneSameVersionDropped(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "p1", Content: "first", Type: "code", Version: 1000})
	store.UpsertPane(Pane{ID: "p1", Content: "second", Type: "code", Version: 1000})

	got := store.GetPane("p1")
	if got.Content != "first" {
		t.Errorf("same version should not overwrite; got %q", got.Content)
	}
}

func TestUpsertPaneHigherVersionWins(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "p1", Content: "old", Type: "code", Version: 1000})
	store.UpsertPane(Pane{ID: "p1", Content: "new", Type: "code", Version: 1001})

	got := store.GetPane("p1")
	if got.Content != "new" || got.Version != 1001 {
		t.Errorf("higher version should win; got content=%q version=%d", got.Content, got.Version)
	}
}

// ---- ReplacePanes ----

func TestReplaceParanesSwapsEntireSet(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "old", Content: "will be replaced", Version: 1})

	store.ReplacePanes([]Pane{
		{ID: "a", Content: "pane a", Version: 10},
		{ID: "b", Content: "pane b", Version: 20},
	})

	if store.GetPane("old") != nil {
		t.Error("old pane should be gone after ReplacePanes")
	}
	if p := store.GetPane("a"); p == nil || p.Content != "pane a" {
		t.Errorf("expected pane a, got %v", p)
	}
	if p := store.GetPane("b"); p == nil || p.Content != "pane b" {
		t.Errorf("expected pane b, got %v", p)
	}
	if panes := store.GetPanes(); len(panes) != 2 {
		t.Errorf("expected 2 panes, got %d", len(panes))
	}
}

// ---- DeletePane ----

func TestDeletePaneMissing(t *testing.T) {
	store := testStore(t)
	// Deleting a nonexistent pane must not panic.
	store.DeletePane("nonexistent")
}

func TestDeletePaneRemovesEntry(t *testing.T) {
	store := testStore(t)

	store.UpsertPane(Pane{ID: "p1", Content: "bye", Version: 1})
	store.DeletePane("p1")

	if store.GetPane("p1") != nil {
		t.Error("pane should not exist after delete")
	}
}

// ---- Store initialization ----

func TestNewStoreCreatesDirectoryAndGeneratesConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store.config.DeviceID == "" {
		t.Error("expected non-empty DeviceID after first-run init")
	}
	if store.config.DeviceName == "" {
		t.Error("expected non-empty DeviceName after first-run init")
	}
	// Config must be persisted so the next startup reuses the same ID.
	if _, err := os.Stat(filepath.Join(store.dir, "config.json")); err != nil {
		t.Errorf("config.json not created: %v", err)
	}
}

func TestNewStoreLoadsExistingConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Bootstrap a config.json before the store is opened.
	tossDir := filepath.Join(tmpHome, ".toss")
	if err := os.MkdirAll(filepath.Join(tossDir, "files"), 0750); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DeviceID: "preset-id", DeviceName: "preset-device"}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(tossDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if store.config.DeviceID != "preset-id" {
		t.Errorf("expected preset-id, got %q", store.config.DeviceID)
	}
	if store.config.DeviceName != "preset-device" {
		t.Errorf("expected preset-device, got %q", store.config.DeviceName)
	}
}

func TestLoadConfigFallsBackOnParseError(t *testing.T) {
	dir := t.TempDir()
	// Write invalid JSON.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{dir: dir, panes: make(map[string]*Pane), filesDir: dir}
	s.loadConfig()
	// normalizeConfig is called, so DeviceID must be non-empty even after parse failure.
	if s.config.DeviceID == "" {
		t.Error("expected non-empty DeviceID after parse failure fallback")
	}
}

func TestNormalizeConfigFillsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir, panes: make(map[string]*Pane), filesDir: dir}
	s.config = Config{} // both empty
	s.normalizeConfig()

	if s.config.DeviceID == "" {
		t.Error("expected DeviceID to be generated")
	}
	if s.config.DeviceName == "" {
		t.Error("expected DeviceName to be generated")
	}
}

func TestNormalizeConfigPreservesExistingFields(t *testing.T) {
	s := &Store{dir: t.TempDir(), panes: make(map[string]*Pane)}
	s.config = Config{DeviceID: "fixed-id", DeviceName: "fixed-name"}
	s.normalizeConfig()

	if s.config.DeviceID != "fixed-id" {
		t.Errorf("DeviceID should not change, got %q", s.config.DeviceID)
	}
	if s.config.DeviceName != "fixed-name" {
		t.Errorf("DeviceName should not change, got %q", s.config.DeviceName)
	}
}

func TestLoadPanesFromDisk(t *testing.T) {
	dir := t.TempDir()
	panes := []Pane{
		{ID: "lp1", Name: "loaded pane", Content: "disk content", Type: "code", Version: 5},
		{ID: "lp2", Name: "second pane", Content: "more content", Type: "code", Version: 10},
	}
	data, _ := json.Marshal(panes)
	if err := os.WriteFile(filepath.Join(dir, "panes.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	s := &Store{dir: dir, panes: make(map[string]*Pane), filesDir: dir}
	s.loadPanes()

	if p := s.GetPane("lp1"); p == nil || p.Content != "disk content" {
		t.Errorf("expected lp1 with 'disk content', got %v", p)
	}
	if p := s.GetPane("lp2"); p == nil || p.Version != 10 {
		t.Errorf("expected lp2 with version 10, got %v", p)
	}
}

func TestLoadPanesIgnoresMissingFile(t *testing.T) {
	s := &Store{dir: t.TempDir(), panes: make(map[string]*Pane)}
	s.loadPanes() // no panes.json → no panic, no panes loaded
	if len(s.GetPanes()) != 0 {
		t.Error("expected empty panes when file is absent")
	}
}

// ---- SSE subscribe / unsubscribe ----

func TestSubscribeSSEReceivesNotification(t *testing.T) {
	node := testNode(t)
	ch := node.subscribeSSE()
	defer node.unsubscribeSSE(ch)

	node.notifySSE()

	select {
	case <-ch:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Error("expected SSE notification within 100ms")
	}
}

func TestUnsubscribeSSEStopsNotifications(t *testing.T) {
	node := testNode(t)
	ch := node.subscribeSSE()
	node.unsubscribeSSE(ch)

	node.notifySSE()

	select {
	case <-ch:
		t.Error("should not receive notification after unsubscribe")
	default:
		// expected
	}
}

// ---- createClipboardPane / createClipboardImagePane ----

func TestCreateClipboardPane(t *testing.T) {
	node := testNode(t)
	node.createClipboardPane("hello clipboard")

	panes := node.store.GetPanes()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}
	p := panes[0]
	if p.Content != "hello clipboard" {
		t.Errorf("expected 'hello clipboard', got %q", p.Content)
	}
	if p.Type != "code" {
		t.Errorf("expected type=code, got %q", p.Type)
	}
	if p.Language != "plaintext" {
		t.Errorf("expected language=plaintext, got %q", p.Language)
	}
	if p.ID == "" {
		t.Error("expected non-empty ID")
	}
	if p.CreatedBy != node.store.config.DeviceID {
		t.Errorf("expected createdBy=%q, got %q", node.store.config.DeviceID, p.CreatedBy)
	}
}

func TestCreateClipboardImagePane(t *testing.T) {
	node := testNode(t)
	imgData := []byte{0xFF, 0xD8, 0xFF, 0xE0} // minimal JPEG-like bytes
	node.createClipboardImagePane(imgData, ".jpg", "screenshot.jpg")

	panes := node.store.GetPanes()
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}
	p := panes[0]
	if p.Language != "markdown" {
		t.Errorf("expected language=markdown, got %q", p.Language)
	}
	if !strings.Contains(p.Content, "/api/files/") {
		t.Errorf("expected /api/files/ reference in content, got %q", p.Content)
	}
	if !strings.Contains(p.Content, ".jpg") {
		t.Errorf("expected .jpg in content, got %q", p.Content)
	}
	// Image file must be written to store.
	if p.Preview == nil || !*p.Preview {
		t.Error("expected preview=true for image pane")
	}
}

// ---- ClipboardMonitor idempotency ----

func TestClipboardMonitorStopWhenNotRunning(t *testing.T) {
	node := testNode(t)
	// Stop on a never-started monitor must not panic.
	node.clipboard.Stop()
	node.clipboard.Stop() // second stop also safe
}

func TestClipboardMonitorStartIdempotent(t *testing.T) {
	// Start twice — the second call must be a no-op (no new goroutine).
	node := testNode(t)
	cm := node.clipboard

	// We can't start the real monitor (it reads system clipboard),
	// but we can verify the guard works by manually setting running=true.
	cm.mu.Lock()
	cm.running = true
	cm.stopCh = make(chan struct{})
	cm.mu.Unlock()

	cm.Start() // should exit immediately without spawning a goroutine

	// Cleanup
	cm.Stop()
}

// ---- createClipboardFilePaneFromRefs edge cases ----

func TestCreateClipboardFilePaneFromRefsEmpty(t *testing.T) {
	node := testNode(t)
	node.createClipboardFilePaneFromRefs(nil)
	if len(node.store.GetPanes()) != 0 {
		t.Error("expected no pane created for empty file list")
	}
}

// ---- prepareClipboardRecvDir ----

func TestPrepareClipboardRecvDirClearsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old1.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old2.txt"), []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := prepareClipboardRecvDir(dir); err != nil {
		t.Fatalf("prepareClipboardRecvDir: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected all files removed, got %d remaining", len(entries))
	}
}

// ---- copyToRecvDir ----

func TestCopyToRecvDirDuplicateFilenameGetsUniqueSuffix(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create a source file.
	src := filepath.Join(srcDir, "report.pdf")
	if err := os.WriteFile(src, []byte("pdf content"), 0600); err != nil {
		t.Fatal(err)
	}

	// First copy — goes to report.pdf.
	dst1, err := copyToRecvDir(dstDir, src, "report.pdf")
	if err != nil {
		t.Fatalf("first copy: %v", err)
	}
	if filepath.Base(dst1) != "report.pdf" {
		t.Errorf("expected 'report.pdf' for first copy, got %q", filepath.Base(dst1))
	}

	// Second copy with same name — must get a unique suffix to avoid overwriting.
	dst2, err := copyToRecvDir(dstDir, src, "report.pdf")
	if err != nil {
		t.Fatalf("second copy: %v", err)
	}
	if dst2 == dst1 {
		t.Error("duplicate copy must produce a different destination path")
	}
	if filepath.Ext(dst2) != ".pdf" {
		t.Errorf("expected .pdf extension, got %q", filepath.Ext(dst2))
	}
}

func TestGetPanesIsIndependentCopy(t *testing.T) {
	store := testStore(t)
	store.UpsertPane(Pane{ID: "p1", Content: "original", Version: 1})

	panes := store.GetPanes()
	panes[0].Content = "mutated"

	// Store must be unaffected by external mutation of the returned slice.
	got := store.GetPane("p1")
	if got.Content != "original" {
		t.Errorf("store content was mutated by caller; got %q", got.Content)
	}
}
