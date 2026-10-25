package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/AlexG-SYS/Test2_ImageProcessing_AlexG_ReeneB/internal/data"
)

// startImageWorker launches the single background worker goroutine for
// the life of the process, which polls the database for new image jobs to claim and process. It
// uses a ticker to poll at a regular interval, which is configured in the application config.

// If there are no jobs to claim yet,
// since createImageHandler doesn't create any.
// image transformation, and MarkCompleted/MarkFailed inside the ticker
func (app *application) startImageWorker(ctx context.Context) {
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		ticker := time.NewTicker(app.config.workerPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				app.logger.Info("image worker stopped")
				return
			case <-ticker.C:
				app.logger.Debug("image worker polling for jobs") // [V2] was Info; logged 4x/second and buried the experiment log lines
				err := app.processNextImageJob(ctx)
				if err != nil && !errors.Is(err, data.ErrRecordNotFound) && !errors.Is(err, context.Canceled) {
					app.logger.Error("image worker error", "error", err)
				}
			}
		}
	}()
}

// processNextImageJob claims the next queued job, processes it, and marks it completed or failed.
// It returns an error if there was a problem claiming the job or marking it completed/failed,
// but not if the job itself failed (that is recorded in the database and logged).
//
// Every database change below now (1) increments the job version inside the
// UPDATE, and (2) is followed by publishJobEvent, which logs the post-commit time
// and wakes waiting long-poll handlers.
func (app *application) processNextImageJob(ctx context.Context) error {
	job, err := app.models.Jobs.ClaimNext(ctx)
	if err != nil {
		return err
	}

	app.logger.Info("image job started", "job_id", job.PublicID)

	// queued -> processing is an observable change (version 1 -> 2): publish it.
	app.publishJobEvent(&data.JobEvent{
		PublicID: job.PublicID, Version: job.Version, Status: job.Status, Stage: job.Stage,
	})

	// If a processing delay is configured, wait for that duration before starting the actual image processing.
	// This simulates a longer processing time for testing purposes.
	// If the context is canceled during this wait, stop the timer and return the context's error.
	// With -processing-delay=30s this IS experiment condition A (quiet job): no updates during the delay.
	if app.config.processingDelay > 0 {
		timer := time.NewTimer(app.config.processingDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	// the simulated phase runs BEFORE the real transformation, and
	// the job only becomes completed after the real transformation succeeds.
	procErr := app.runSimulatedPhase(ctx, job)

	// Call transformImage to perform the actual image processing. If it returns an error,
	// log the error and mark the job as failed with a safe error message for the client.
	if procErr == nil {
		procErr = app.transformImage(ctx, job)
	}
	if procErr != nil {
		if ctx.Err() != nil {
			return ctx.Err() // shutting down: leave the job as it is
		}
		app.logger.Error("image job failed", "job_id", job.PublicID, "error", procErr)
		ev, markErr := app.models.Jobs.MarkFailed(ctx, job.ID, safeProcessingError(procErr))
		if markErr != nil {
			return markErr
		}
		app.publishJobEvent(ev) // observers learn about the failure (terminal version)
		return nil
	}

	ev, err := app.models.Jobs.MarkCompleted(ctx, job.ID)
	if err != nil {
		return err
	}
	app.publishJobEvent(ev) // terminal commit: this log line is the "server terminal-commit time"
	app.logger.Info("image job completed", "job_id", job.PublicID)
	return nil
}

// publishJobEvent runs IMMEDIATELY AFTER a transaction commits.
// Order matters: the log line is written first (its commit_us is the timestamp
// used for completion-detection delay), then waiting observers are woken.
// The notification only means "re-read the database"; it never carries state.
func (app *application) publishJobEvent(ev *data.JobEvent) {
	app.logger.Info("job_commit",
		"job_id", ev.PublicID,
		"version", ev.Version,
		"status", ev.Status,
		"stage", ev.Stage,
		"variants_completed", ev.VariantsCompleted,
		"simulation_step", ev.SimulationStep,
		// Wall-clock Unix time in microseconds, taken right after the commit.
		// Resolution is 1 microsecond; real accuracy is limited by scheduling (~ms).
		"commit_us", time.Now().UnixMicro(),
	)
	app.notifier.Notify(ev.PublicID)
}

// runSimulatedPhase is the EXPERIMENTAL workload. It is OFF unless
// -sim-duration is set. It performs duration/interval steps; each step stores
// simulation_step and increments the version, exactly like real progress would.
// It never changes variants_completed and never completes the job.
func (app *application) runSimulatedPhase(ctx context.Context, job *data.Job) error {
	duration, interval := app.config.simDuration, app.config.simInterval
	if duration <= 0 || interval <= 0 {
		return nil // disabled: ordinary use
	}
	steps := int(duration / interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for step := 1; step <= steps; step++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		ev, err := app.models.Jobs.UpdateSimulation(ctx, job.ID, step)
		if err != nil {
			return fmt.Errorf("simulated step %d: %w", step, err)
		}
		app.publishJobEvent(ev) // actual commit time is logged, not the scheduled time
	}
	return nil
}

// variantStage maps a REAL variant to its stage name (PROG-02).
func variantStage(name string) string {
	switch name {
	case "thumbnail":
		return "thumbnail_ready"
	case "preview":
		return "preview_ready"
	default:
		return "display_ready"
	}
}

// transformImage retrieves the original image from storage, decodes it, generates the required variants,
// and saves them to storage and the database. It returns an error if any step fails.
func (app *application) transformImage(ctx context.Context, job *data.Job) error {
	img, err := app.models.Images.GetByID(job.ImageID)
	if err != nil {
		return fmt.Errorf("look up image: %w", err)
	}

	srcPath := filepath.Join(app.config.storage.originalsDir, img.StoredFilename)
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open original: %w", err)
	}
	defer srcFile.Close()

	decoded, _, err := image.Decode(srcFile)
	if err != nil {
		return fmt.Errorf("decode original: %w", err)
	}

	if err := os.MkdirAll(app.config.storage.variantsDir, 0o755); err != nil {
		return fmt.Errorf("prepare variants directory: %w", err)
	}

	for i, spec := range requiredVariants { // index i = how many variants are stored once this one is recorded
		out := generateVariant(decoded, spec)
		bounds := out.Bounds()

		storedFilename, err := randomFilename(extensionFor(img.MediaType))
		if err != nil {
			return fmt.Errorf("generate variant filename: %w", err)
		}

		destPath := filepath.Join(app.config.storage.variantsDir, storedFilename)
		size, err := writeImage(destPath, out, img.MediaType)
		if err != nil {
			return fmt.Errorf("write %s variant: %w", spec.name, err)
		}

		err = app.models.Variants.Insert(job.ImageID, spec.name, storedFilename, bounds.Dx(), bounds.Dy(), size)
		if err != nil {
			os.Remove(destPath) // don't leave an orphaned file if the database insert fails
			return fmt.Errorf("record %s variant: %w", spec.name, err)
		}

		// the variant FILE and its METADATA row are stored, so the
		// count may now increase. The count therefore never exceeds what exists.
		// Status stays "processing" until MarkCompleted.
		ev, err := app.models.Jobs.UpdateProgress(ctx, job.ID, i+1, variantStage(spec.name))
		if err != nil {
			return fmt.Errorf("record %s progress: %w", spec.name, err)
		}
		app.publishJobEvent(ev)
	}

	return nil
}

// writeImage writes the given image to the specified destination path in the appropriate format based on the media type.
func writeImage(destPath string, img image.Image, mediaType string) (int64, error) {
	f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	switch mediaType {
	case "image/png":
		err = png.Encode(f, img)
	default: // image/jpeg — the only other type VAL-01 accepts
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 85})
	}
	if err != nil {
		os.Remove(destPath)
		return 0, err
	}

	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func extensionFor(mediaType string) string {
	if mediaType == "image/png" {
		return ".png"
	}
	return ".jpg"
}

// safeProcessingError returns a user-friendly error message based on the underlying error encountered during image processing.
func safeProcessingError(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, data.ErrRecordNotFound):
		return "The source image could not be found"
	case errors.Is(err, os.ErrNotExist):
		return "The source image file is missing"
	default:
		return "The image could not be processed"
	}
}
