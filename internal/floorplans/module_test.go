package floorplans_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/spaceweave/internal/floorplans"
	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/dxf/dxftest"
)

func moduleOptions(t *testing.T) floorplans.Options {
	t.Helper()
	dir := t.TempDir()
	validator, err := filepath.Abs("../../web/floorplan-iso/tools/validate.mjs")
	if err != nil {
		t.Fatal(err)
	}
	return floorplans.Options{
		Inbox: filepath.Join(dir, "inbox"), Output: filepath.Join(dir, "generated"),
		Node: "node", Validator: validator, Workers: 1, Settle: time.Millisecond, Tracer: "auto",
	}
}

func TestModuleTracesReviewsAndReloadsFloorplan(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required for the real floorplan validator")
	}
	opts := moduleOptions(t)
	source := filepath.Join(t.TempDir(), "office.dxf")
	if err := os.WriteFile(source, dxftest.Office(4).Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	module, err := floorplans.New(opts, floorplans.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	draft, created, err := module.Trace(ctx, source)
	if err != nil || !created || draft.Status != api.Draft {
		t.Fatalf("trace did not publish a draft: %+v created=%v err=%v", draft, created, err)
	}
	if draft.Tracer != "auto" || draft.Attempts != 1 || draft.Error != "" {
		t.Fatalf("unexpected processing outcome: %+v", draft)
	}
	for _, name := range []string{"trace.json", "plan.json", "overlay.png", "report.json", draft.Source, draft.Drawing} {
		if name == "" {
			t.Fatal("drawing conversion did not retain its rendered source")
		}
		data, err := os.ReadFile(filepath.Join(opts.Output, draft.ID, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("missing published artifact %s: %v", name, err)
		}
		if strings.HasSuffix(name, ".json") && !json.Valid(data) {
			t.Fatalf("published artifact %s is not JSON", name)
		}
	}
	duplicate, created, err := module.Trace(ctx, source)
	if err != nil || created || duplicate.ID != draft.ID || duplicate.Attempts != draft.Attempts ||
		duplicate.Status != api.Draft {
		t.Fatalf(
			"duplicate import changed the trace: %+v created=%v err=%v",
			duplicate,
			created,
			err,
		)
	}
	was, approved, err := module.Plans().Review(draft.ID, api.Approve, "")
	if err != nil || was != api.Draft || approved.Status != api.Approved {
		t.Fatalf("review failed: was=%s plan=%+v err=%v", was, approved, err)
	}
	reopened, err := floorplans.Open(opts.Output)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Get(draft.ID)
	if err != nil || loaded.Status != api.Approved || loaded.Attempts != 1 {
		t.Fatalf("approval did not survive reload: %+v %v", loaded, err)
	}
	listed, err := reopened.List()
	if err != nil || len(listed) != 1 || listed[0].ID != draft.ID {
		t.Fatalf("reloaded catalogue lost or duplicated the floorplan: %+v %v", listed, err)
	}
	if module.Profiles() == nil {
		t.Fatal("module did not supply profiles for review and tracing")
	}
}

func TestModuleRejectsMissingTracingPrerequisites(t *testing.T) {
	for _, prerequisite := range []string{"node", "validator"} {
		t.Run(prerequisite, func(t *testing.T) {
			opts := moduleOptions(t)
			want := "validator script"
			if prerequisite == "node" {
				opts.Node = filepath.Join(t.TempDir(), "missing-node")
				want = "node is needed"
			} else {
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				opts.Node = executable
				opts.Validator = filepath.Join(t.TempDir(), "missing-validator.mjs")
			}
			if _, err := floorplans.New(
				opts,
				floorplans.Dependencies{},
			); err == nil ||
				!strings.Contains(err.Error(), want) {
				t.Fatalf("missing %s was not rejected: %v", prerequisite, err)
			}
		})
	}
}
