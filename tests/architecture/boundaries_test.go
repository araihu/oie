package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
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
				// Emoji validation needs Unicode grapheme boundaries. This pure
				// text utility is an application dependency, not an adapter port.
				segmentation := layer == "application" && p == "github.com/rivo/uniseg"
				if strings.HasPrefix(p, "net/http") || strings.HasPrefix(p, "database/sql") ||
					(strings.Contains(p, ".") && !strings.HasPrefix(p, "github.com/araihu/oie/internal/domain") && !segmentation) {
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

func TestGraphemeUtilityDoesNotImportAdapters(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "github.com/rivo/uniseg").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect grapheme dependency: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if dependency == "github.com/rivo/uniseg" {
			continue
		}
		if strings.Contains(dependency, ".") || dependency == "net" || strings.HasPrefix(dependency, "net/") || strings.HasPrefix(dependency, "database/") {
			t.Errorf("grapheme utility imported non-text dependency %s", dependency)
		}
	}
}

func TestCLIBuildGraphDoesNotImportEngineOrPresentation(t *testing.T) {
	output, err := exec.Command("go", "-C", "../../cli", "list", "-deps", "./cmd/oiectl").CombinedOutput()
	if err != nil {
		t.Fatalf("CLI dependency graph: %v\n%s", err, output)
	}
	for _, dependency := range strings.Fields(string(output)) {
		if strings.HasPrefix(dependency, "github.com/araihu/oie/internal/") || strings.HasPrefix(dependency, "mellium.im/") || strings.HasPrefix(dependency, "github.com/wailsapp/") || strings.HasPrefix(dependency, "modernc.org/sqlite") {
			t.Errorf("CLI imports server dependency %s", dependency)
		}
	}
}
