package api

import "io"

// Asset is an owned floorplan artifact. The caller closes Reader after use.
type Asset struct {
	Reader      io.ReadCloser
	ContentType string
	Name        string
}

// Assets retrieves approved artifact kinds and known floor-package attachments.
// Callers never receive a storage path or choose an arbitrary file name.
type Assets interface {
	Artifact(id, kind string) (Asset, error)
	Attachment(id, sha string) (Asset, error)
}
