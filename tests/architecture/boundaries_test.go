package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoreAndPresentationImportBoundaries(t *testing.T) {
	for _, layer := range []string{"domain", "application"} {
		root := filepath.Join("..", "..", "internal", layer)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, im := range f.Imports {
				p, _ := strconv.Unquote(im.Path.Value)
				if strings.HasPrefix(p, "net/http") || strings.HasPrefix(p, "database/sql") ||
					(strings.Contains(p, ".") && !strings.HasPrefix(p, "github.com/araihu/oie/internal/domain")) {
					t.Errorf("%s imports adapter/third-party package %s", path, p)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	paths, _ := filepath.Glob("../../internal/adapters/web/*.go")
	for _, path := range paths {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			if strings.Contains(p, "oie/internal/application") || strings.Contains(p, "oie/internal/domain") ||
				strings.Contains(p, "oie/internal/adapters/sqlite") || strings.Contains(p, "goshtoso/site") {
				t.Errorf("BFF bypasses engine API: %s", p)
			}
		}
	}
}
