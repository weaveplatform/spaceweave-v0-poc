// Package core owns Floorplans lifecycle decisions independently of storage and transport.
package core

import (
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
)

var transitions = map[api.Status][]api.Status{
	api.Queued:     {api.Converting, api.Tracing},
	api.Converting: {api.Tracing, api.Failed, api.Queued},
	api.Tracing:    {api.Draft, api.Failed, api.Queued},
	api.Draft:      {api.Approved, api.Queued},
	api.Approved:   {api.Queued},
	api.Failed:     {api.Queued},
}

var reviews = map[api.Action]struct {
	from []api.Status
	to   api.Status
}{
	api.Approve: {[]api.Status{api.Draft}, api.Approved},
	api.Reject:  {[]api.Status{api.Draft, api.Approved}, api.Queued},
	api.Retry:   {[]api.Status{api.Failed}, api.Queued},
	api.Retrace: {[]api.Status{api.Draft, api.Failed}, api.Queued},
}

// Transition updates lifecycle state after validating the current state.
// The caller must serialize the decision and persistence of the changed entry.
func Transition(e *api.Entry, to api.Status, now func() time.Time) error {
	if !slices.Contains(transitions[e.Status], to) {
		return fmt.Errorf("%w: %s is %s, can't become %s", api.ErrTransition, e.ID, e.Status, to)
	}
	e.Status = to
	e.UpdatedAt = now().UTC()
	e.Progress = nil
	return nil
}

// ValidateAction rejects an unknown action before any storage lookup is needed.
func ValidateAction(action api.Action) error {
	if _, ok := reviews[action]; !ok {
		return fmt.Errorf("%w: %q", api.ErrAction, action)
	}
	return nil
}

// Review applies a review decision within the caller's atomic update.
func Review(e *api.Entry, action api.Action, note string, now func() time.Time) error {
	if err := ValidateAction(action); err != nil {
		return err
	}
	r := reviews[action]
	if !slices.Contains(r.from, e.Status) {
		return fmt.Errorf("%w: %s is %s, can't %s it", api.ErrTransition, e.ID, e.Status, action)
	}
	e.Status = r.to
	e.UpdatedAt = now().UTC()
	e.Progress = nil
	switch action {
	case api.Reject:
		e.Note = note
	case api.Retry:
		e.Error = ""
	case api.Retrace:
		e.Error, e.Note = "", ""
	case api.Approve:
	}
	return nil
}

// Requeue allows attachment edits only before processing or after a draft/failure.
func Requeue(e *api.Entry, now func() time.Time) error {
	if e.Status == api.Queued {
		return nil
	}
	if !slices.Contains(reviews[api.Retrace].from, e.Status) {
		return fmt.Errorf("%w: %s is %s; its files can change once it is a draft or has failed",
			api.ErrTransition, e.ID, e.Status)
	}
	e.Status = api.Queued
	e.UpdatedAt = now().UTC()
	e.Progress = nil
	e.Error, e.Note = "", ""
	return nil
}
