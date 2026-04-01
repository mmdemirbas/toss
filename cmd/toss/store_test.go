package main

import "testing"

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
