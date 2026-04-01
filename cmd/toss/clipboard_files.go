package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	clipboardMaxFileSize  = 50 << 20 // 50 MB per file
	clipboardMaxFileCount = 20       // max files per clipboard copy
)

// readClipboardFiles reads file paths from the system clipboard.
// Returns nil when no files are present.
func readClipboardFiles() ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardFilesDarwin()
	case "linux":
		return readClipboardFilesLinux()
	default:
		return nil, nil
	}
}

// writeClipboardFiles writes file paths to the system clipboard.
func writeClipboardFiles(paths []string) error {
	switch runtime.GOOS {
	case "darwin":
		return writeClipboardFilesDarwin(paths)
	case "linux":
		return writeClipboardFilesLinux(paths)
	default:
		return fmt.Errorf("clipboard files not supported on %s", runtime.GOOS)
	}
}

// hashFilePaths returns a stable hash of sorted file paths for change detection.
func hashFilePaths(paths []string) string {
	sorted := make([]string, len(paths))
	copy(sorted, paths)
	sort.Strings(sorted)
	h := md5.Sum([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:])
}

// ──────────────────────────────────────────────
// macOS
// ──────────────────────────────────────────────

func readClipboardFilesDarwin() ([]string, error) {
	// Use AppleScriptObjC to read file URLs from NSPasteboard.
	// This handles both single and multiple file copies reliably.
	script := `
use framework "AppKit"
use scripting additions

set pb to current application's NSPasteboard's generalPasteboard()
set fileType to current application's NSPasteboardTypeFileURL
if (pb's availableTypeFromArray:{fileType}) is missing value then return ""

set urls to pb's readObjectsForClasses:{current application's NSURL} options:(missing value)
if urls is missing value then return ""

set cnt to (urls's |count|()) as integer
if cnt = 0 then return ""

set pathList to ""
repeat with i from 1 to cnt
	set u to (urls's objectAtIndex:(i - 1))
	if (u's isFileURL()) as boolean then
		set p to (u's |path|()) as text
		set pathList to pathList & p & linefeed
	end if
end repeat
return pathList
`
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return nil, nil
	}
	return parsePathList(string(out)), nil
}

func writeClipboardFilesDarwin(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString("use framework \"AppKit\"\nuse scripting additions\n\n")
	sb.WriteString("set pb to current application's NSPasteboard's generalPasteboard()\n")
	sb.WriteString("pb's clearContents()\n")
	sb.WriteString("set urls to current application's NSMutableArray's new()\n")
	for _, p := range paths {
		escaped := strings.ReplaceAll(p, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		fmt.Fprintf(&sb, "urls's addObject:(current application's NSURL's fileURLWithPath:\"%s\")\n", escaped)
	}
	sb.WriteString("pb's writeObjects:urls\n")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "osascript", "-e", sb.String()).Run()
}

// ──────────────────────────────────────────────
// Linux  (X11 via xclip, Wayland via wl-paste)
// ──────────────────────────────────────────────

func readClipboardFilesLinux() ([]string, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		return readClipboardFilesXclip()
	}
	if _, err := exec.LookPath("wl-paste"); err == nil {
		return readClipboardFilesWayland()
	}
	return nil, nil
}

func readClipboardFilesXclip() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	targets, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
	if err != nil {
		return nil, nil
	}
	if !strings.Contains(string(targets), "text/uri-list") {
		return nil, nil
	}

	data, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "text/uri-list", "-o").Output()
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return parseURIList(string(data)), nil
}

func readClipboardFilesWayland() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	types, err := exec.CommandContext(ctx, "wl-paste", "--list-types").Output()
	if err != nil || !strings.Contains(string(types), "text/uri-list") {
		return nil, nil
	}

	data, err := exec.CommandContext(ctx, "wl-paste", "--type", "text/uri-list").Output()
	if err != nil || len(data) == 0 {
		return nil, nil
	}
	return parseURIList(string(data)), nil
}

func writeClipboardFilesLinux(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	uriList := buildURIList(paths)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "text/uri-list", "-i")
		cmd.Stdin = strings.NewReader(uriList)
		return cmd.Run()
	}

	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.CommandContext(ctx, "wl-copy", "--type", "text/uri-list")
		cmd.Stdin = strings.NewReader(uriList)
		return cmd.Run()
	}

	return fmt.Errorf("no clipboard tool found (need xclip or wl-copy)")
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

// parsePathList parses newline-separated file paths.
func parsePathList(s string) []string {
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		p := strings.TrimSpace(line)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// parseURIList parses text/uri-list format (file:// URIs separated by \r\n).
func parseURIList(s string) []string {
	var paths []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue // comment or empty line per RFC 2483
		}
		if strings.HasPrefix(line, "file://") {
			u, err := url.Parse(line)
			if err != nil {
				continue
			}
			if u.Path != "" {
				paths = append(paths, filepath.FromSlash(u.Path))
			}
		}
	}
	return paths
}

// buildURIList creates a text/uri-list string from file paths.
func buildURIList(paths []string) string {
	var sb strings.Builder
	for _, p := range paths {
		u := url.URL{Scheme: "file", Path: p}
		sb.WriteString(u.String())
		sb.WriteString("\r\n")
	}
	return sb.String()
}
