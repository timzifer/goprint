package virtualprinter

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/timzifer/goprint"
)

// Job is a job printed to a virtual printer. Its fields do not change
// after Print returned; the state does.
type Job struct {
	ID       int
	Printer  string
	Title    string
	Settings goprint.Settings
	// PDF is the document as printed; raster documents are wrapped into
	// a PDF with one image per page.
	PDF      []byte
	Warnings []goprint.Warning
	// Attributes are the document's attributes ([goprint.Document]), also
	// when they came over IPP.
	Attributes map[string]string

	mu    sync.Mutex
	state goprint.JobState
	done  chan struct{} // closed once state is final
}

// State returns the job's current state.
func (j *Job) State() goprint.JobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// SetState moves the job to st, e.g. a held job (see
// [Provider.HoldJobs]) to processing and then completed or aborted. A
// final state cannot be left; SetState then reports an error.
func (j *Job) SetState(st goprint.JobState) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state.Done() {
		return fmt.Errorf("virtualprinter: job %d is already %s", j.ID, j.state)
	}
	j.state = st
	if st.Done() {
		close(j.done)
	}
	return nil
}

// handle is the [goprint.JobHandle] of a Job.
type handle struct{ j *Job }

func (h handle) ID() string { return strconv.Itoa(h.j.ID) }

func (h handle) State(context.Context) (goprint.JobState, error) { return h.j.State(), nil }

func (h handle) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.j.done:
	}
	switch st := h.j.State(); st {
	case goprint.JobCanceled:
		return goprint.ErrCanceled
	case goprint.JobAborted:
		return fmt.Errorf("virtualprinter: job %d on %s aborted", h.j.ID, h.j.Printer)
	}
	return nil
}

func (h handle) Cancel(context.Context) error {
	return h.j.SetState(goprint.JobCanceled)
}
