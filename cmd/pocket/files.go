package main

import (
	"context"
	"os"

	"github.com/egoist/mygo"
)

func pickFiles(_ context.Context) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach files", Multiple: true})
}

// Dialog.Save marshals onto the native UI thread; this function runs on a worker.
func saveFile(_ context.Context, name string, data []byte) (bool, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "Export session", DefaultPath: name, Filters: []mygo.FileFilter{{Name: "Markdown", Extensions: []string{"md"}}}})
	if err != nil || path == "" {
		return false, err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return false, err
	}
	return true, nil
}
