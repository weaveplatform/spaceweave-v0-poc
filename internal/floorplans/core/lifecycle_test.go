package core

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
)

func TestReviewLifecycle(t *testing.T) {
	now := time.Date(2026, time.November, 27, 12, 30, 0, 0, time.FixedZone("office", 3600))
	for _, tt := range []struct {
		name       string
		from       api.Status
		action     api.Action
		to         api.Status
		note, fail string
	}{
		{"approve draft", api.Draft, api.Approve, api.Approved, "previous note", "previous failure"},
		{"reject draft", api.Draft, api.Reject, api.Queued, "review feedback", "previous failure"},
		{"reject approval", api.Approved, api.Reject, api.Queued, "review feedback", "previous failure"},
		{"retry failure", api.Failed, api.Retry, api.Queued, "previous note", ""},
		{"retrace draft", api.Draft, api.Retrace, api.Queued, "", ""},
		{"retrace failure", api.Failed, api.Retrace, api.Queued, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := api.Entry{
				ID: "office", Status: tt.from, Note: "previous note", Error: "previous failure",
				Progress: &api.Progress{Turn: 3}, Attempts: 2, CreatedAt: now.Add(-time.Hour),
			}
			if err := Review(
				&entry,
				tt.action,
				"review feedback",
				func() time.Time { return now },
			); err != nil {
				t.Fatal(err)
			}
			if entry.Status != tt.to || entry.Note != tt.note || entry.Error != tt.fail ||
				entry.Progress != nil {
				t.Fatalf("unexpected review result: %+v", entry)
			}
			if entry.UpdatedAt != now.UTC() || entry.CreatedAt != now.Add(-time.Hour) ||
				entry.Attempts != 2 {
				t.Fatalf("review changed processing/history fields: %+v", entry)
			}
		})
	}
}

func TestRefusedReviewDoesNotChangeEntry(t *testing.T) {
	for _, tt := range []struct {
		status api.Status
		action api.Action
		want   error
		text   string
	}{
		{api.Failed, api.Approve, api.ErrTransition, "office is failed, can't approve it"},
		{api.Tracing, api.Reject, api.ErrTransition, "office is tracing, can't reject it"},
		{api.Draft, api.Retry, api.ErrTransition, "office is draft, can't retry it"},
		{api.Approved, api.Retrace, api.ErrTransition, "office is approved, can't retrace it"},
		{api.Draft, "unknown", api.ErrAction, `"unknown"`},
	} {
		t.Run(string(tt.status)+"/"+string(tt.action), func(t *testing.T) {
			entry := api.Entry{ID: "office", Status: tt.status, Note: "keep", Error: "keep"}
			before := entry
			err := Review(&entry, tt.action, "replace", func() time.Time {
				t.Fatal("a refused review must not advance the clock")
				return time.Time{}
			})
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.text) {
				t.Fatalf("got %v, want %v with %q", err, tt.want, tt.text)
			}
			if !reflect.DeepEqual(entry, before) {
				t.Fatalf("refused review changed the entry: %+v", entry)
			}
		})
	}
}

func TestProcessingLifecycleAndRejectedTransition(t *testing.T) {
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	entry := api.Entry{ID: "office", Status: api.Queued, Note: "keep", Error: "keep"}
	for _, to := range []api.Status{api.Converting, api.Tracing, api.Draft, api.Approved, api.Queued, api.Tracing, api.Failed, api.Queued} {
		entry.Progress = &api.Progress{Turn: 4}
		if err := Transition(&entry, to, func() time.Time { return now }); err != nil {
			t.Fatal(err)
		}
		if entry.Status != to || entry.UpdatedAt != now || entry.Progress != nil ||
			entry.Note != "keep" ||
			entry.Error != "keep" {
			t.Fatalf("unexpected transition result: %+v", entry)
		}
	}
	before := entry
	err := Transition(&entry, api.Approved, func() time.Time {
		t.Fatal("a refused transition must not advance the clock")
		return time.Time{}
	})
	if !errors.Is(err, api.ErrTransition) ||
		!strings.Contains(err.Error(), "office is queued, can't become approved") {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if !reflect.DeepEqual(before, entry) {
		t.Fatal("refused transition changed the entry")
	}
}

func TestAttachmentRequeue(t *testing.T) {
	now := time.Date(2026, time.November, 27, 12, 0, 0, 0, time.UTC)
	for _, status := range []api.Status{api.Queued, api.Draft, api.Failed, api.Converting, api.Tracing, api.Approved} {
		t.Run(string(status), func(t *testing.T) {
			entry := api.Entry{
				ID:       "office",
				Status:   status,
				Error:    "old failure",
				Note:     "old note",
				Progress: &api.Progress{Turn: 2},
			}
			before := entry
			err := Requeue(&entry, func() time.Time { return now })
			switch status {
			case api.Queued:
				if err != nil || !reflect.DeepEqual(entry, before) {
					t.Fatalf(
						"editing a queued package must preserve its lifecycle: %+v %v",
						entry,
						err,
					)
				}
			case api.Draft, api.Failed:
				if err != nil || entry.Status != api.Queued || entry.Error != "" ||
					entry.Note != "" ||
					entry.Progress != nil ||
					entry.UpdatedAt != now {
					t.Fatalf("package change was not queued cleanly: %+v %v", entry, err)
				}
			default:
				if !errors.Is(err, api.ErrTransition) || !reflect.DeepEqual(entry, before) {
					t.Fatalf("protected package changed: %+v %v", entry, err)
				}
			}
		})
	}
}
