package storage

import (
	"io"
	"os"
)

type Storage interface {
	Open(path string) (File, error)
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	RemoveAll(path string) error
	OpenFile(path string, flag int, perm os.FileMode) (File, error)
	Exists(path string) bool
}

type File interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
	Readdir(count int) ([]os.FileInfo, error)
}
