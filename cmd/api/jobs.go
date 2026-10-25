package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/AlexG-SYS/Test2_ImageProcessing_AlexG_ReeneB/internal/data"
)

const (
	longPollMaxWaitSeconds     = 20
	longPollDefaultWaitSeconds = 20
	// Long Polling: extra time granted on top of the hold so the response can be written.
	longPollWriteGrace = 10 * time.Second
)

// Checking the format first means /v1/jobs/abc is a
// prompt 404 instead of a PostgreSQL "invalid input syntax for type uuid" 500.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var digitsPattern = regexp.MustCompile(`^[0-9]+$`)

// Long Polling observationParams holds the parsed ?after=&wait= query.
type observationParams struct {
	longPoll bool          // true when `after` was supplied
	after    int           // the version the client already has
	wait     time.Duration // maximum holding time
}

// Long Polling parseObservationParams parses the ?after=&wait= query parameters and returns an observationParams struct.
// Returns an error if the parameters are invalid.
func parseObservationParams(r *http.Request) (observationParams, error) {
	q := r.URL.Query()
	hasAfter, hasWait := q.Has("after"), q.Has("wait")

	if !hasAfter && !hasWait {
		return observationParams{}, nil // plain short-poll request
	}
	if !hasAfter {
		return observationParams{}, errors.New("wait requires the after parameter")
	}

	rawAfter := q.Get("after")
	if !digitsPattern.MatchString(rawAfter) {
		return observationParams{}, errors.New("after must be a non-negative integer")
	}
	after, err := strconv.Atoi(rawAfter)
	if err != nil {
		return observationParams{}, errors.New("after must be a non-negative integer")
	}

	waitSeconds := longPollDefaultWaitSeconds // wait defaults to 20 when after is supplied
	if hasWait {
		rawWait := q.Get("wait")
		if !digitsPattern.MatchString(rawWait) {
			return observationParams{}, errors.New("wait must be an integer from 1 to 20")
		}
		waitSeconds, err = strconv.Atoi(rawWait)
		if err != nil || waitSeconds < 1 || waitSeconds > longPollMaxWaitSeconds {
			return observationParams{}, errors.New("wait must be an integer from 1 to 20")
		}
	}

	return observationParams{
		longPoll: true,
		after:    after,
		wait:     time.Duration(waitSeconds) * time.Second,
	}, nil
}

// getJobHandler handles GET /v1/jobs/:id requests. It supports both prompt and long-polling observation modes.
// Prompt mode returns the current job snapshot immediately, while long-polling mode holds the request until the job changes or the wait time expires.
func (app *application) getJobHandler(w http.ResponseWriter, r *http.Request) {
	//  observation responses must never be cached.
	w.Header().Set("Cache-Control", "no-store")

	publicID := r.PathValue("id")
	if !uuidPattern.MatchString(publicID) {
		app.notFoundResponse(w, r)
		return
	}

	params, err := parseObservationParams(r)
	if err != nil {
		app.badRequestResponse(w, r, err)
		return
	}

	if params.longPoll {
		app.longPollJob(w, r, publicID, params)
		return
	}
	// Prompt mode: return the current job snapshot immediately.
	job, err := app.models.Jobs.GetByPublicID(r.Context(), publicID)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(w, r)
			return
		}
		app.serverErrorResponse(w, r, err)
		return
	}
	app.writeJobSnapshot(w, r, job)
}

// Long Polling: the longPollJob function implements the long-polling observation mode for GET /v1/jobs/:id requests.
// It holds the request until the job changes or the wait time expires, returning a 200 with the new snapshot or a 204 if no change occurred.
func (app *application) longPollJob(w http.ResponseWriter, r *http.Request, publicID string, p observationParams) {
	started := time.Now()
	dbReads := 0
	iteration := 0
	endReason := "error" // overwritten on every exit path below

	// One structured line per long poll: the raw server-side evidence for the report.
	defer func() {
		app.logger.Info("long_poll",
			"job_id", publicID,
			"after", p.after,
			"wait_s", int(p.wait/time.Second),
			"end_reason", endReason,
			"held_ms", time.Since(started).Milliseconds(),
			"db_reads", dbReads,
			"jobs_with_waiters", app.notifier.ActiveJobs(),
		)
	}()

	// Long Polling: set a write deadline to ensure the response can be sent before the connection is closed.
	// This is important because the request may be held for up to p.wait seconds, and we want to avoid
	// a situation where the client has already closed the connection but the server is still trying to write a response.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(p.wait + longPollWriteGrace))

	// subscribe to the job's change channel. This allows the server to be notified when the job changes,
	// so it can return a new snapshot to the client.
	sub := app.notifier.Subscribe(publicID)
	defer sub.Release() //released on every exit path

	// a timer, not a busy loop; stopped on exit.
	deadline := time.NewTimer(p.wait)
	defer deadline.Stop()

	// load reads the authoritative row. It returns done=true when it already
	// wrote a response (404/500) or the client is gone, so the caller must return.
	load := func() (job *data.Job, done bool) {
		dbReads++
		job, err := app.models.Jobs.GetByPublicID(r.Context(), publicID)
		if err == nil {
			return job, false
		}
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			endReason = "not_found"
			app.notFoundResponse(w, r) // unknown job: prompt 404
		case r.Context().Err() != nil:
			endReason = "cancelled" // request cancelled while querying; nothing to send
		default:
			endReason = "error"
			app.serverErrorResponse(w, r, err) // never disguise an error as 204
		}
		return nil, true
	}

	for {
		// The loop is the long-polling observation: it reads the authoritative row, checks if the client is behind,
		// and either returns a snapshot or holds until a change occurs or the wait time expires.
		changed := sub.Channel()

		job, done := load()
		if done {
			return
		}

		// A client can never be "ahead" of the database: reject promptly, do not wait.
		if p.after > job.Version {
			endReason = "after_too_high"
			app.badRequestResponse(w, r, errors.New("after must not exceed the current version"))
			return
		}

		// Terminal jobs answer immediately even when versions match, so reopening
		// a finished job never hangs.
		if job.Terminal() {
			endReason = "terminal"
			app.writeJobSnapshot(w, r, job)
			return
		}

		// The client is behind: answer immediately with the current snapshot.
		if job.Version > p.after {
			if iteration == 0 {
				endReason = "newer_immediate" // changed BEFORE the request arrived
			} else {
				endReason = "changed" // changed WHILE the request was held
			}
			app.writeJobSnapshot(w, r, job)
			return
		}

		// Versions match and the job is active: hold.
		iteration++
		select {
		case <-changed:
			// A signal is NOT a representation: loop around and re-read PostgreSQL.
			continue

		case <-deadline.C:
			// Holding deadline: recheck authoritative state BEFORE choosing 204.
			job, done := load()
			if done {
				return
			}
			if job.Terminal() || job.Version > p.after {
				endReason = "changed_at_deadline"
				app.writeJobSnapshot(w, r, job)
				return
			}
			endReason = "timeout_204"
			w.WriteHeader(http.StatusNoContent) // normal "no change": no JSON body
			return

		case <-r.Context().Done():
			// cancelling observation ends THIS handler only.
			// The worker's job is in the database and is not affected.
			endReason = "cancelled"
			return
		}
	}
}

// writeJobSnapshot builds the full representation and writes it as 200.
// Shared by the prompt path and the long-poll path so both modes return the
// same fields and formatting.
func (app *application) writeJobSnapshot(w http.ResponseWriter, r *http.Request, job *data.Job) {
	response, err := app.jobRepresentation(r.Context(), job)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		app.serverErrorResponse(w, r, err)
		return
	}
	if err := app.writeJSON(w, http.StatusOK, response, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// jobRepresentation is the Version 1 response-building code, moved into a
// function so that both observation modes use it, plus the new fields.
func (app *application) jobRepresentation(ctx context.Context, job *data.Job) (envelope, error) {
	response := envelope{
		"id":           job.PublicID,
		"image_id":     job.ImageID, // overwritten below with the public ID
		"status":       job.Status,
		"queued_at":    job.QueuedAt,
		"started_at":   job.StartedAt,
		"completed_at": job.CompletedAt,
		// new fields present in EVERY 200 snapshot.
		"version":            job.Version,
		"stage":              job.Stage,
		"variants_completed": job.VariantsCompleted,
		"simulation_step":    job.SimulationStep, // new field for simulation progress
	}

	// Retrieve the image associated with the job to get its public ID
	img, err := app.models.Images.GetByID(job.ImageID)
	if err != nil {
		return nil, err
	}
	response["image_id"] = img.PublicID

	// If the job has failed, include the error message in the response.
	if job.Status == "failed" && job.ErrorMessage != nil {
		response["error"] = *job.ErrorMessage
	}

	// Only a COMPLETED job exposes variants, so partial results never look like a finished job (PROG-03).
	if job.Status == "completed" {
		variants, err := app.models.Variants.ListByImageID(job.ImageID, img.PublicID)
		if err != nil {
			return nil, err
		}
		response["variants"] = variants
	}
	return response, nil
}
