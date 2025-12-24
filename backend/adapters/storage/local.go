package storage

import (
	"os"
)

type LocalStorage struct {
	Root string
}

func NewLocalStorage(root string) *LocalStorage {
	return &LocalStorage{Root: root}
}

func (s *LocalStorage) Open(path string) (File, error) {
	return os.Open(path)
}

func (s *LocalStorage) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (s *LocalStorage) ReadDir(path string) ([]os.FileInfo, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, len(entries))
	for i, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		infos[i] = info
	}
	return infos, nil
}

func (s *LocalStorage) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (s *LocalStorage) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (s *LocalStorage) OpenFile(path string, flag int, perm os.FileMode) (File, error) {
	return os.OpenFile(path, flag, perm)
}

func (s *LocalStorage) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}
