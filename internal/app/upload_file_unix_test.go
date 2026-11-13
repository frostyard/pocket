//go:build linux || darwin

package app

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAttachmentFIFORejectedWithoutWriter(t *testing.T) {
	a, loop := teamApp(t)
	a.current = 7
	a.composer.reset(7)
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	a.attachFiles(7, []string{path})
	loop.RunUntil(t, func() bool { return !a.composer.files[7][0].uploading })
	if !strings.Contains(a.composer.files[7][0].err, "regular file") {
		t.Fatal("FIFO accepted")
	}
}
