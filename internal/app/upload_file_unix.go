//go:build linux || darwin

package app

import (
	"os"
	"syscall"
)

// Nonblocking open prevents a path replaced by a FIFO from waiting for a writer.
// The caller validates the opened descriptor before reading any bytes.
func openUploadFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
