package catalog

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
)

var _ api.Assets = (*Store)(nil)

// Artifact resolves a fixed artifact kind through the owning plan entry.
func (s *Store) Artifact(id, kind string) (api.Asset, error) {
	e, err := s.Get(id)
	if err != nil {
		return api.Asset{}, err
	}
	name := map[string]string{
		"model": "plan.json", "trace": "trace.json", "report": "report.json",
		"source": e.Source, "overlay": "overlay.png", "drawing": e.Drawing, "original": e.Upload,
	}[kind]
	if name == "" {
		return api.Asset{}, api.ErrNotFound
	}
	downloadName := filepath.Base(name)
	if kind == "original" {
		downloadName = filepath.Base(e.Original)
	}
	return s.asset(id, name, downloadName)
}

// Attachment retrieves only a file present in the plan's package manifest.
func (s *Store) Attachment(id, sha string) (api.Asset, error) {
	e, err := s.Get(id)
	if err != nil {
		return api.Asset{}, err
	}
	for _, f := range e.Files {
		if f.SHA256 == sha {
			return s.asset(id, filepath.FromSlash(f.Upload), filepath.Base(f.Name))
		}
	}
	return api.Asset{}, api.ErrNotFound
}

func (s *Store) asset(id, name, downloadName string) (api.Asset, error) {
	if id == "." || id == ".." || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) {
		return api.Asset{}, api.ErrNotFound
	}
	root, err := os.OpenRoot(s.Dir)
	if err != nil {
		return api.Asset{}, api.ErrNotFound
	}
	defer root.Close() //nolint:errcheck // read-only root handle
	planRoot, err := root.OpenRoot(id)
	if err != nil {
		return api.Asset{}, api.ErrNotFound
	}
	defer planRoot.Close() //nolint:errcheck // read-only root handle
	f, err := planRoot.Open(name)
	if err != nil {
		return api.Asset{}, api.ErrNotFound
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return api.Asset{}, api.ErrNotFound
	}
	contentType := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".webp": "image/webp", ".svg": "image/svg+xml", ".json": "application/json",
	}[strings.ToLower(filepath.Ext(name))]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return api.Asset{Reader: f, ContentType: contentType, Name: downloadName}, nil
}
