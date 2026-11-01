// Package archtest enforces the boundary between chat and search, so the search
// service stays independently deployable. Both sides may share gen/ and
// internal/platform/.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/frangte/mnchat/"

var (
	searchDirs = []string{"cmd/mnchat-search", "internal/search"}
	chatDirs   = []string{
		"cmd/mnchat", "internal/cmd", "internal/config", "internal/domain", "internal/httpapi",
		"internal/infra", "internal/realtime", "internal/repo", "internal/searchclient", "internal/service",
	}
)

func TestSearchDoesNotImportChat(t *testing.T) {
	var forbidden []string
	for _, dir := range chatDirs {
		if strings.HasPrefix(dir, "internal/") {
			forbidden = append(forbidden, module+dir)
		}
	}
	checkImports(t, searchDirs, forbidden)
}

func TestChatDoesNotImportSearch(t *testing.T) {
	checkImports(t, chatDirs, []string{module + "internal/search"})
}

func checkImports(t *testing.T, dirs, forbidden []string) {
	t.Helper()
	for _, dir := range dirs {
		root := filepath.Join("..", "..", dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue // the service may not exist yet
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				for _, bad := range forbidden {
					if imported == bad || strings.HasPrefix(imported, bad+"/") {
						t.Errorf("%s imports %s, which crosses the chat/search boundary", path, imported)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}
