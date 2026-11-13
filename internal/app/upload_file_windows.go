package app

import "os"

func openUploadFile(path string) (*os.File, error) { return os.Open(path) }
