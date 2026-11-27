// Package generation verifies committed REST outputs independently of Git's index.
package generation

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrStale = errors.New("generated REST outputs are stale; run make generate")

// Outputs names the committed bundles and wire code, including newly added files.
func Outputs() []string {
	var paths []string
	for _, surface := range []string{"api", "operator"} {
		paths = append(
			paths,
			"api/"+surface+".openapi.yaml",
			"internal/transport/gen/"+surface+"/server.gen.go",
			"pkg/client/"+surface+"/client.gen.go",
		)
	}
	return paths
}

// Verify compares bytes before and after generation, so untracked outputs are
// checked too. A failed generation is reported before comparing partial output.
func Verify(root string, generate func() error) error {
	before := map[string][]byte{}
	for _, path := range Outputs() {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("read generated output %s: %w", path, err)
		}
		before[path] = b
	}
	if err := generate(); err != nil {
		return fmt.Errorf("generate REST outputs: %w", err)
	}
	for _, path := range Outputs() {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("read regenerated output %s: %w", path, err)
		}
		if !bytes.Equal(before[path], b) {
			return fmt.Errorf("%w: %s", ErrStale, path)
		}
	}
	return nil
}
