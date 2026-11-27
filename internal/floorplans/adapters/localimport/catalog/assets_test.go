package catalog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedArtifactsAndAttachments(t *testing.T) {
	store, _ := open(t)
	entry, _, err := store.Ingest(image(t, "office.png", "source pixels"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ name, data string }{
		{"plan.json", `{"SOURCE":{"url":"generated/old/source.png"}}`},
		{"trace.json", "trace"},
		{"report.json", "report"},
		{"overlay.png", "overlay"},
		{"drawing.svg", "drawing"},
	} {
		if err := os.WriteFile(
			filepath.Join(store.PlanDir(entry.ID), file.name),
			[]byte(file.data),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Update(func(c *Catalog) error {
		c.Plans[0].Drawing = "drawing.svg"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"model", "trace", "report", "source", "overlay", "drawing", "original"} {
		asset, err := store.Artifact(entry.ID, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		data, err := io.ReadAll(asset.Reader)
		_ = asset.Reader.Close()
		if err != nil || len(data) == 0 || asset.Name == "" ||
			strings.ContainsAny(asset.Name, `/\`) ||
			asset.ContentType == "" {
			t.Fatalf("invalid asset %s: %+v %v", kind, asset, err)
		}
		if kind == "original" && asset.Name != "office.png" {
			t.Fatalf("original filename lost: %s", asset.Name)
		}
	}
	attached, added, err := store.Attach(
		entry.ID,
		image(t, "reference.png", "reference pixels"),
		RoleReference,
	)
	if err != nil || !added {
		t.Fatalf("attach: %+v %v %v", attached, added, err)
	}
	file := attached.Attached(RoleReference)[0]
	asset, err := store.Attachment(entry.ID, file.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Reader.Close()
	data, err := io.ReadAll(asset.Reader)
	if err != nil || string(data) != "reference pixels" || asset.Name != "reference.png" {
		t.Fatalf("attachment: %s %+v %v", data, asset, err)
	}
}

func TestAssetRefusesUnknownAndEscapingFiles(t *testing.T) {
	store, _ := open(t)
	entry, _, err := store.Ingest(image(t, "office.png", "source pixels"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"unknown", "drawing", "trace", "../../catalog.json"} {
		if _, err := store.Artifact(entry.ID, kind); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unexpected %s asset: %v", kind, err)
		}
	}
	if _, err := store.Artifact("missing", "source"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing plan: %v", err)
	}
	if _, err := store.Attachment(entry.ID, strings.Repeat("a", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown attachment: %v", err)
	}
	secret := image(t, "private.txt", "secret outside the plan")
	if err := os.Symlink(secret, filepath.Join(store.PlanDir(entry.ID), "trace.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.Artifact(entry.ID, "trace"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("symlink escaped the plan: %v", err)
	}
	if err := store.Update(func(c *Catalog) error {
		c.Plans[0].Source = "../catalog.json"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Artifact(entry.ID, "source"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("metadata escaped the plan: %v", err)
	}
}

func TestCADAssetsUsePortableOctetStream(t *testing.T) {
	store, _ := open(t)
	entry, _, err := store.Ingest(image(t, "drawing.dxf", "original drawing bytes"))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := store.Artifact(entry.ID, "original")
	if err != nil {
		t.Fatal(err)
	}
	_ = asset.Reader.Close()
	if asset.ContentType != "application/octet-stream" || asset.Name != "drawing.dxf" {
		t.Fatalf("CAD original has a platform-dependent media type: %+v", asset)
	}
	attached, _, err := store.Attach(
		entry.ID,
		image(t, "reference.dwg", "reference drawing bytes"),
		RoleReference,
	)
	if err != nil {
		t.Fatal(err)
	}
	asset, err = store.Attachment(entry.ID, attached.Attached(RoleReference)[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	_ = asset.Reader.Close()
	if asset.ContentType != "application/octet-stream" || asset.Name != "reference.dwg" {
		t.Fatalf("CAD attachment has a platform-dependent media type: %+v", asset)
	}
}
