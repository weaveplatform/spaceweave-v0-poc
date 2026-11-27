package generation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestVerify(t *testing.T) {
	failure := errors.New("generator failed")
	for _, tc := range []struct {
		name                       string
		change, remove             bool
		generationError, errorWant error
	}{
		{name: "current"},
		{name: "stale", change: true, errorWant: ErrStale},
		{name: "failed", generationError: failure, errorWant: failure},
		{name: "removed", remove: true, errorWant: os.ErrNotExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, p := range Outputs() {
				path := filepath.Join(root, p)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := Verify(root, func() error {
				if tc.generationError != nil {
					return tc.generationError
				}
				path := filepath.Join(root, Outputs()[0])
				if tc.remove {
					return os.Remove(path)
				}
				if tc.change {
					return os.WriteFile(path, []byte("new"), 0o600)
				}
				return nil
			})
			if !errors.Is(err, tc.errorWant) {
				t.Fatalf("got %v, want %v", err, tc.errorWant)
			}
		})
	}
}

func TestVerifyRequiresOutputs(t *testing.T) {
	if err := Verify(
		t.TempDir(),
		func() error { t.Fatal("must not generate over missing baseline"); return nil },
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatal(err)
	}
}
