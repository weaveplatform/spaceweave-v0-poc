// Package api publishes the Floorplans command, query and lifecycle contracts.
package api

import (
	"errors"
	"time"
)

// Status is where a plan is in its life.
type Status string

// The statuses. Their string values are read by js/plans/index.js and
// js/pipeline.js.
const (
	Queued     Status = "queued"
	Converting Status = "converting"
	Tracing    Status = "tracing"
	Draft      Status = "draft"
	Approved   Status = "approved"
	Failed     Status = "failed"
)

// Errors.
var (
	ErrNotFound   = errors.New("no such plan")
	ErrTransition = errors.New("status change not allowed")
	ErrLocked     = errors.New("catalog is locked by another process")
	ErrFormat     = errors.New("not a format spaceweave reads")
	ErrRole       = errors.New("a file attached to a plan is a space plan or a reference")
	ErrPackage    = errors.New("that file can't join this plan")
	ErrPrimary    = errors.New("the plan's own drawing can't be removed; delete the plan instead")
)

// Entry describes a plan and its processing/review state.
type Entry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	// Source is the image the tracer reads, inside the plan's directory;
	// empty for a drawing until it has been converted.
	Source string `json:"source"`
	// Upload is the file as it arrived, inside the plan's directory: the
	// same as Source for an image, original.<ext> for a drawing.
	Upload string `json:"upload"`
	Format string `json:"format"` // a convert.Format ID
	// Files are the plan's floor package: the drawing above, as role
	// RoleArchitecture, first, then any space plan and reference files
	// attached to it. An entry written before packages existed has none on
	// disk and gets its drawing here when it is loaded.
	Files      []File   `json:"files,omitempty"`
	Drawing    string   `json:"drawing,omitempty"` // the drawing as SVG, once converted
	Scale      *Scale   `json:"scale,omitempty"`   // known from the drawing, once converted
	Original   string   `json:"original"`          // the inbox file it came from
	SHA256     string   `json:"sha256"`
	Confidence *float64 `json:"confidence,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	// Tracer identifies the automatic or Claude tracing implementation.
	Tracer    string    `json:"tracer,omitempty"`
	CostUSD   float64   `json:"costUsd"`
	Attempts  int       `json:"attempts"`
	Note      string    `json:"note,omitempty"` // reviewer feedback for the next attempt
	Error     string    `json:"error,omitempty"`
	Progress  *Progress `json:"progress,omitempty"` // set only while tracing
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// File is one file of a plan's floor package.
type File struct {
	Role string `json:"role"` // RoleArchitecture, RoleSpaces or RoleReference
	// Upload is the file inside the plan's directory: Entry.Upload for the
	// drawing, files/<role>-<sha6>.<ext> for one attached.
	Upload string `json:"upload"`
	Format string `json:"format"` // a convert.Format ID
	SHA256 string `json:"sha256"`
	Name   string `json:"name"` // as it arrived
}

// File roles. Their string values are read by js/pipeline.js and sent by
// js/api.js.
const (
	// RoleArchitecture is the drawing traced: the plan's own upload.
	RoleArchitecture = "architecture"
	// RoleSpaces is a space plan of the same floor: its area outlines,
	// numbers and names are what the rooms are. A plan has at most one,
	// so attaching another replaces it.
	RoleSpaces = "spaces"
	// RoleReference is kept with the plan for the reviewer and not read
	// by the tracer: a PDF print, a photo of the floor.
	RoleReference = "reference"
)

// Primary is the plan's drawing as a package file.
func (e *Entry) Primary() File {
	return File{
		Role:   RoleArchitecture,
		Upload: e.Upload,
		Format: e.Format,
		SHA256: e.SHA256,
		Name:   e.Original,
	}
}

// Attached are the package's files of one role.
func (e *Entry) Attached(role string) []File {
	var out []File
	for _, f := range e.Files {
		if f.Role == role {
			out = append(out, f)
		}
	}
	return out
}

// Scale is what a drawing says about its own scale.
type Scale struct {
	// PxPerMetre is the image's scale on the building; 0 when unknown.
	PxPerMetre float64 `json:"pxPerMetre"`
	// Assumed means the drawing didn't state its units and they were
	// guessed from its size.
	Assumed bool `json:"assumed,omitempty"`
	// PxPerPaperMM is the image's scale on paper, for a drawing measured
	// on a sheet rather than the building.
	PxPerPaperMM float64 `json:"pxPerPaperMm,omitempty"`
}

// Exact reports whether the scale can be trusted without checking.
func (s *Scale) Exact() bool { return s != nil && s.PxPerMetre > 0 && !s.Assumed }

// Progress is how far a trace has got, for the site's pipeline view.
type Progress struct {
	Turn    int     `json:"turn"`
	Submits int     `json:"submits"`
	CostUSD float64 `json:"costUsd"`
}

// Action is a reviewer's decision on a plan.
type Action string

// The review actions, shared by `spaceweave plans` and the site's buttons.
const (
	Approve Action = "approve"
	Reject  Action = "reject"
	Retry   Action = "retry"
	// Retrace traces a plan again with no note: what changed is how its
	// drawing is read (a layer's role, in the client profile), not
	// anything for the tracer to be told.
	Retrace Action = "retrace"
)

// ErrAction is an unknown review action.
var ErrAction = errors.New("unknown review action")

// Service is the supported Floorplans command and query surface.
// Paths identify staged local inputs; the implementation retains their bytes.
type Service interface {
	List() ([]Entry, error)
	Get(id string) (Entry, error)
	Ingest(path string) (Entry, bool, error)
	Review(id string, action Action, note string) (Status, Entry, error)
	Attach(id, path, role string) (Entry, bool, error)
	Detach(id, sha string) (Entry, error)
	PlanOf(path string) (Entry, error)
}
