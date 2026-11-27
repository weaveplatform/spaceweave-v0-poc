package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListReturnsEntriesWithoutPersistenceEnvelope(t *testing.T) {
	store, dir := open(t)
	entries, err := store.List()
	if err != nil || entries == nil || len(entries) != 0 {
		t.Fatalf("empty catalogue: %+v %v", entries, err)
	}
	entry, _, err := store.Ingest(image(t, "office.png", "floorplan"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err = store.List()
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("listed entries: %+v %v", entries, err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "catalog.json"),
		[]byte("invalid"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if entries, err = store.List(); err == nil || entries != nil {
		t.Fatalf("malformed catalogue returned entries: %+v %v", entries, err)
	}
}
