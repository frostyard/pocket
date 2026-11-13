package main

import (
	"context"
	"os"

	"github.com/egoist/mygo"
	"github.com/frostyard/pocket/internal/app"
)

func pickFiles(_ context.Context) ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "Attach files", Multiple: true})
}

// Dialog.Save marshals onto the native UI thread; this function runs on a worker.
func saveFile(_ context.Context, spec app.SaveFileOptions, data []byte) (bool, error) {
	options := mygo.SaveDialogOptions{Title: spec.Title, DefaultPath: spec.Name}
	if len(spec.Extensions) > 0 {
		options.Filters = []mygo.FileFilter{{Name: spec.Filter, Extensions: spec.Extensions}}
	}
	path, err := mygo.Dialog.Save(options)
	if err != nil || path == "" {
		return false, err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return false, err
	}
	return true, nil
}
