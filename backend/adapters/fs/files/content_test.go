package files

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/gtsteffaniak/filebrowser/backend/adapters/storage"
	"github.com/gtsteffaniak/filebrowser/backend/common/settings"
	"github.com/gtsteffaniak/filebrowser/backend/indexing"
	"github.com/stretchr/testify/require"
)

func TestGetContent_UTF8Truncation(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	testFilePath := filepath.Join(cwd, "..", "..", "..", "..", "frontend", "tests", "playwright-files", "utf8-truncated.txt")

	if _, err = os.Stat(testFilePath); os.IsNotExist(err) {
		testFilePath = filepath.Join("frontend", "tests", "playwright-files", "utf8-truncated.txt")
		if _, err = os.Stat(testFilePath); os.IsNotExist(err) {
			t.Skipf("Test file not found at %s, skipping test", testFilePath)
			return
		}
	}

	absPath, err := filepath.Abs(testFilePath)
	require.NoError(t, err)

	t.Run("file with UTF-8 truncation at 4096 byte boundary", func(t *testing.T) {
		idx := &indexing.Index{
			Source:  settings.Source{Path: filepath.Dir(absPath)},
			Storage: storage.NewLocalStorage(filepath.Dir(absPath)),
		}
		content, err := getContent(idx, absPath)
		require.NoError(t, err)
		require.NotEmpty(t, content)
		require.Contains(t, content, "文件已备份")
		require.Contains(t, content, "2024年")
	})

	t.Run("regular UTF-8 text file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-utf8-*.txt")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		testText := "Hello, 世界! This is a test file with UTF-8 characters.\n"
		_, err = tmpFile.WriteString(testText)
		require.NoError(t, err)
		tmpFile.Close()

		idx := &indexing.Index{
			Source:  settings.Source{Path: filepath.Dir(tmpFile.Name())},
			Storage: storage.NewLocalStorage(filepath.Dir(tmpFile.Name())),
		}
		content, err := getContent(idx, tmpFile.Name())
		require.NoError(t, err)
		require.Equal(t, testText, content)
	})

	t.Run("file smaller than header size", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-small-*.txt")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		testText := "Small file content"
		_, err = tmpFile.WriteString(testText)
		require.NoError(t, err)
		tmpFile.Close()

		idx := &indexing.Index{
			Source:  settings.Source{Path: filepath.Dir(tmpFile.Name())},
			Storage: storage.NewLocalStorage(filepath.Dir(tmpFile.Name())),
		}
		content, err := getContent(idx, tmpFile.Name())
		require.NoError(t, err)
		require.Equal(t, testText, content)
	})

	t.Run("file with Chinese characters at boundary", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-chinese-*.txt")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		baseText := "2024年 06月 17日 星期一 04:05:58 CST 文件已备份\n"
		content := ""
		for len([]byte(content)) < 4094 {
			content += baseText
		}
		encoded := []byte(content)
		if len(encoded) > 4094 {
			encoded = encoded[:4094]
		}
		for len(encoded) > 0 {
			lastRune, _ := decodeLastRune(encoded)
			if lastRune != 0xFFFD {
				break
			}
			encoded = encoded[:len(encoded)-1]
		}

		_, err = tmpFile.Write(encoded)
		require.NoError(t, err)
		tmpFile.Close()

		idx := &indexing.Index{
			Source:  settings.Source{Path: filepath.Dir(tmpFile.Name())},
			Storage: storage.NewLocalStorage(filepath.Dir(tmpFile.Name())),
		}
		result, err := getContent(idx, tmpFile.Name())
		require.NoError(t, err)
		require.NotEmpty(t, result)
		require.Equal(t, string(encoded), result)
	})
}

func decodeLastRune(p []byte) (rune, int) {
	if len(p) == 0 {
		return 0, 0
	}
	r, size := utf8.DecodeLastRune(p)
	return r, size
}
