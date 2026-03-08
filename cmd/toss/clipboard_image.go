package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// readClipboardImage reads image data from the system clipboard.
// Returns a content hash, the raw image bytes, the file extension (e.g. ".png"),
// and any error.  Returns empty hash when no image is present.
func readClipboardImage() (hash string, data []byte, ext string, err error) {
	switch runtime.GOOS {
	case "darwin":
		return readClipboardImageDarwin()
	case "linux":
		return readClipboardImageLinux()
	default:
		return "", nil, "", nil
	}
}

// writeClipboardImage copies an image file into the system clipboard.
func writeClipboardImage(filePath string) error {
	switch runtime.GOOS {
	case "darwin":
		return writeClipboardImageDarwin(filePath)
	case "linux":
		return writeClipboardImageLinux(filePath)
	default:
		return fmt.Errorf("clipboard image not supported on %s", runtime.GOOS)
	}
}

// hashBytes returns the hex-encoded MD5 of data.
func hashBytes(data []byte) string {
	h := md5.Sum(data)
	return hex.EncodeToString(h[:])
}

// ──────────────────────────────────────────────
// macOS
// ──────────────────────────────────────────────

func readClipboardImageDarwin() (string, []byte, string, error) {
	tmpBase := filepath.Join(os.TempDir(), fmt.Sprintf("toss_clip_%d", os.Getpid()))

	// Single osascript call: try PNGf, then TIFF. Writes data to a temp
	// file and returns the extension so Go can read it back.
	script := fmt.Sprintf(`
try
	set imgData to (the clipboard as «class PNGf»)
	set ext to "png"
on error
	try
		set imgData to (the clipboard as «class TIFF»)
		set ext to "tiff"
	on error
		return ""
	end try
end try
set filePath to "%s." & ext
set f to open for access (POSIX file filePath) with write permission
set eof f to 0
write imgData to f
close access f
return ext
`, tmpBase)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return "", nil, "", nil
	}
	ext := strings.TrimSpace(string(out))
	if ext != "png" && ext != "tiff" {
		return "", nil, "", nil
	}

	tmpFile := tmpBase + "." + ext
	defer os.Remove(tmpFile)

	data, err := os.ReadFile(tmpFile)
	if err != nil || len(data) == 0 {
		return "", nil, "", nil
	}

	return hashBytes(data), data, "." + ext, nil
}

func writeClipboardImageDarwin(filePath string) error {
	ext := strings.ToLower(filepath.Ext(filePath))
	cls := "PNGf"
	if ext == ".tiff" || ext == ".tif" {
		cls = "TIFF"
	}
	script := fmt.Sprintf(`set the clipboard to (read (POSIX file "%s") as «class %s»)`, filePath, cls)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

// ──────────────────────────────────────────────
// Linux  (X11 via xclip, Wayland via wl-paste)
// ──────────────────────────────────────────────

func readClipboardImageLinux() (string, []byte, string, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		return readClipboardImageXclip()
	}
	if _, err := exec.LookPath("wl-paste"); err == nil {
		return readClipboardImageWayland()
	}
	return "", nil, "", nil
}

func readClipboardImageXclip() (string, []byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	targets, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
	if err != nil {
		return "", nil, "", nil
	}
	if !strings.Contains(string(targets), "image/png") {
		return "", nil, "", nil
	}

	data, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
	if err != nil || len(data) == 0 {
		return "", nil, "", nil
	}
	return hashBytes(data), data, ".png", nil
}

func readClipboardImageWayland() (string, []byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	types, err := exec.CommandContext(ctx, "wl-paste", "--list-types").Output()
	if err != nil || !strings.Contains(string(types), "image/png") {
		return "", nil, "", nil
	}

	data, err := exec.CommandContext(ctx, "wl-paste", "--type", "image/png").Output()
	if err != nil || len(data) == 0 {
		return "", nil, "", nil
	}
	return hashBytes(data), data, ".png", nil
}

func writeClipboardImageLinux(filePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := exec.LookPath("xclip"); err == nil {
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-i")
		cmd.Stdin = f
		return cmd.Run()
	}

	if _, err := exec.LookPath("wl-copy"); err == nil {
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "wl-copy", "--type", "image/png")
		cmd.Stdin = f
		return cmd.Run()
	}

	log.Println("[clipboard] no image clipboard tool found (need xclip or wl-copy)")
	return fmt.Errorf("no clipboard image tool found")
}
