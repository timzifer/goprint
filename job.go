package goprint

import "context"

// JobState is the state of a print job.
type JobState int

const (
	JobPending JobState = iota
	JobProcessing
	JobCompleted
	JobCanceled
	JobAborted
)

func (s JobState) String() string {
	switch s {
	case JobPending:
		return "pending"
	case JobProcessing:
		return "processing"
	case JobCompleted:
		return "completed"
	case JobCanceled:
		return "canceled"
	case JobAborted:
		return "aborted"
	}
	return "unknown"
}

// Done reports whether the state is final.
func (s JobState) Done() bool {
	return s == JobCompleted || s == JobCanceled || s == JobAborted
}

// jobBackend is implemented by each platform backend.
type jobBackend interface {
	id() string
	state(ctx context.Context) (JobState, error)
	wait(ctx context.Context) error
	cancel(ctx context.Context) error
}

// Job is a submitted print job.
type Job struct {
	b        jobBackend
	warnings []Warning
}

// ID returns the platform job id (IPP job-id or spooler job id).
func (j *Job) ID() string { return j.b.id() }

// State queries the current job state.
func (j *Job) State(ctx context.Context) (JobState, error) { return j.b.state(ctx) }

// Wait blocks until the job reaches a final state or ctx is done.
func (j *Job) Wait(ctx context.Context) error { return j.b.wait(ctx) }

// Cancel cancels the job.
func (j *Job) Cancel(ctx context.Context) error { return j.b.cancel(ctx) }

// Warnings lists settings that could not be honored.
func (j *Job) Warnings() []Warning {
	return append([]Warning(nil), j.warnings...)
}
