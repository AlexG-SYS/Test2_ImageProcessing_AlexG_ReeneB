package data

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Job represents a single image processing job, with metadata about its status and timestamps for each stage of processing.
type Job struct {
	ID           string     `json:"-"`
	PublicID     string     `json:"id"`
	ImageID      string     `json:"image_id"`
	Status       string     `json:"status"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	QueuedAt     time.Time  `json:"queued_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	// New fields for versioning and progress tracking
	Version           int    `json:"version"`            // incremented on every change to the job record, for optimistic concurrency control
	Stage             string `json:"stage"`              // current stage of processing (e.g., "queued", "processing", "completed", "failed")
	VariantsCompleted int    `json:"variants_completed"` // number of variants that have been successfully processed
	SimulationStep    int    `json:"simulation_step"`    // current step in the simulation phase, if any
}

// Terminal returns true if the job is in a terminal state (completed or failed), meaning no further processing will occur.
func (j *Job) Terminal() bool {
	return j.Status == "completed" || j.Status == "failed"
}

// JobEvent represents a change in the state of a job, which can be published to notify any long-polling handlers that the job has changed.
type JobEvent struct {
	PublicID          string
	Version           int
	Status            string
	Stage             string
	VariantsCompleted int
	SimulationStep    int
}

type JobModel struct {
	DB *sql.DB
}

// Insert creates a new job record in the database for the given image ID and returns the created Job struct with its
// ID, PublicID, and QueuedAt fields populated.
func (m JobModel) Insert(imageID string) (*Job, error) {
	// RETURNING now also yields version/stage.
	query := `
		INSERT INTO jobs (image_id)
		VALUES ($1)
		RETURNING id, public_id, image_id, status, queued_at, version, stage`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var job Job
	err := m.DB.QueryRowContext(ctx, query, imageID).Scan(
		&job.ID, &job.PublicID, &job.ImageID, &job.Status, &job.QueuedAt, &job.Version, &job.Stage,
	)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetByPublicID retrieves a job record from the database by its public ID. If no record is found, it returns ErrRecordNotFound.
func (m JobModel) GetByPublicID(ctx context.Context, publicID string) (*Job, error) {
	// SELECT list extended with version, stage, variants_completed, simulation_step.
	query := `
		SELECT id, public_id, image_id, status, error_message, queued_at, started_at, completed_at,
		       version, stage, variants_completed, simulation_step
		FROM jobs
		WHERE public_id = $1`

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var job Job
	err := m.DB.QueryRowContext(ctx, query, publicID).Scan(
		&job.ID, &job.PublicID, &job.ImageID, &job.Status,
		&job.ErrorMessage, &job.QueuedAt, &job.StartedAt, &job.CompletedAt,
		&job.Version, &job.Stage, &job.VariantsCompleted, &job.SimulationStep,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return &job, nil
}

// ClaimNext retrieves the next queued job from the database, marks it as started, and returns the Job struct. If no queued jobs are available, it returns nil without an error.
func (m JobModel) ClaimNext(ctx context.Context) (*Job, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// SELECT the next queued job, locking it for update to prevent other workers from claiming it.
	// The FOR UPDATE SKIP LOCKED clause allows multiple workers to run concurrently without blocking each other.
	query := `
		SELECT id, public_id, image_id, status, queued_at
		FROM jobs
		WHERE status = 'queued'
		ORDER BY queued_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`

	var job Job
	err = tx.QueryRowContext(ctx, query).Scan(&job.ID, &job.PublicID, &job.ImageID, &job.Status, &job.QueuedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}

	// queued -> processing is an observable change, so the version
	// is incremented in the same statement (same transaction) as the status.
	err = tx.QueryRowContext(ctx,
		`UPDATE jobs
		 SET status = 'processing', started_at = now(), version = version + 1, stage = 'starting'
		 WHERE id = $1
		 RETURNING version, stage`, job.ID).Scan(&job.Version, &job.Stage)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	job.Status = "processing"
	return &job, nil
}

// scanEvent is a small helper shared by the write methods below.
func scanEvent(row *sql.Row) (*JobEvent, error) {
	var ev JobEvent
	err := row.Scan(&ev.PublicID, &ev.Version, &ev.Status, &ev.Stage, &ev.VariantsCompleted, &ev.SimulationStep)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// The WHERE clause rejected the update: the job is missing or no longer active.
			return nil, ErrJobNotActive
		}
		return nil, err
	}
	return &ev, nil
}

// UpdateProgress updates the number of completed variants and the current stage of a processing job, increments the version, and returns a JobEvent representing the change. It only succeeds if the job is currently in the "processing" state.
func (m JobModel) UpdateProgress(ctx context.Context, id string, count int, stage string) (*JobEvent, error) {
	return scanEvent(m.DB.QueryRowContext(ctx, `
		UPDATE jobs
		SET variants_completed = $2, stage = $3, version = version + 1
		WHERE id = $1 AND status = 'processing'
		RETURNING public_id, version, status, stage, variants_completed, simulation_step`,
		id, count, stage))
}

// UpdateSimulation updates the simulation step of a processing job, increments the version, and returns a JobEvent representing the change. It only succeeds if the job is currently in the "processing" state.
func (m JobModel) UpdateSimulation(ctx context.Context, id string, step int) (*JobEvent, error) {
	return scanEvent(m.DB.QueryRowContext(ctx, `
		UPDATE jobs
		SET simulation_step = $2, version = version + 1
		WHERE id = $1 AND status = 'processing'
		RETURNING public_id, version, status, stage, variants_completed, simulation_step`,
		id, step))
}

// MarkCompleted marks a processing job as completed, updates its status and completion time, and returns a JobEvent representing the change. It only succeeds if the job is currently in the "processing" state.
func (m JobModel) MarkCompleted(ctx context.Context, id string) (*JobEvent, error) {
	return scanEvent(m.DB.QueryRowContext(ctx, `
		UPDATE jobs
		SET status = 'completed', completed_at = now(), version = version + 1,
		    stage = 'display_ready', variants_completed = 3
		WHERE id = $1 AND status = 'processing'
		RETURNING public_id, version, status, stage, variants_completed, simulation_step`,
		id))
}

// MarkFailed marks a processing job as failed, updates its status and error message, and returns a JobEvent representing the change. It only succeeds if the job is currently in the "processing" state.
func (m JobModel) MarkFailed(ctx context.Context, id, message string) (*JobEvent, error) {
	return scanEvent(m.DB.QueryRowContext(ctx, `
		UPDATE jobs
		SET status = 'failed', error_message = $2, completed_at = now(), version = version + 1
		WHERE id = $1 AND status IN ('queued', 'processing')
		RETURNING public_id, version, status, stage, variants_completed, simulation_step`,
		id, message))
}
