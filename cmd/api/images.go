package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AlexG-SYS/Test2_ImageProcessing_AlexG_ReeneB/internal/data"
)

const maxUploadBytes = 10 << 20 // 10 MB, enforced server-side regardless of what the browser already checked

// allowedMediaTypes maps a sniffed MIME type to the extension that should be used for the stored file.
// This is a whitelist of the only types we accept, and the only extensions we will use for storage.
var allowedMediaTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
}

// It does NOT create a job, does NOT return 202, and does NOT touch
// image_variants. Those all depend on a worker that doesn't exist yet
func (app *application) createImageHandler(w http.ResponseWriter, r *http.Request) {
	// Cap the whole request body before parsing the multipart form, so an
	// oversized upload is rejected without ever being fully buffered.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1<<20) // small cushion for the rest of the request headers and form data, which are not part of the file itself

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		app.badRequestResponse(w, r, fmt.Errorf("Could not parse upload: %w", err))
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		app.badRequestResponse(w, r, errors.New(`An image file is required in the "image" field`))
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		app.badRequestResponse(w, r, errors.New("Image must not exceed 10 MB"))
		return
	}

	// Sniff the real content type from the file's own bytes.
	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && err != io.EOF {
		app.serverErrorResponse(w, r, err)
		return
	}
	mediaType := http.DetectContentType(sniff[:n])

	ext, ok := allowedMediaTypes[mediaType]
	if !ok {
		app.badRequestResponse(w, r, fmt.Errorf("Unsupported file type %q: only JPEG and PNG are accepted", mediaType))
		return
	}

	// Rewind after sniffing so the full file (including the bytes we just read) can be copied to the destination.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Generate a random filename for the stored file, using the correct extension for the MIME type.
	storedFilename, err := randomFilename(ext)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	// Ensure the originals directory exists, creating it if necessary.
	if err := os.MkdirAll(app.config.storage.originalsDir, 0o755); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	destPath := filepath.Join(app.config.storage.originalsDir, storedFilename)

	// Open the destination file for writing, but fail if it already exists (should never happen with a random filename).
	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	defer dest.Close()

	written, err := io.Copy(dest, file)
	if err != nil {
		os.Remove(destPath) // don't leave a partial file behind
		app.serverErrorResponse(w, r, err)
		return
	}

	// Create a durable record of the uploaded image in the database, so it can be referenced later.
	image := &data.Image{
		OriginalFilename: header.Filename,
		StoredFilename:   storedFilename,
		MediaType:        mediaType,
		SizeBytes:        written,
	}

	if err := app.models.Images.Insert(image); err != nil {
		os.Remove(destPath) // don't leave an orphaned file if the durable record fails
		app.serverErrorResponse(w, r, err)
		return
	}

	// Create a job for the image processing worker to pick up and process the uploaded image.
	job, err := app.models.Jobs.Insert(image.ID)
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}

	statusURL := fmt.Sprintf("/v1/jobs/%s", job.PublicID)
	headers := make(http.Header)
	headers.Set("Location", statusURL)

	// Return a 201 Created response with the image's public ID and other metadata.
	err = app.writeJSON(w, http.StatusAccepted, envelope{
		"image_id":   image.PublicID,
		"job_id":     job.PublicID,
		"status":     job.Status,
		"version":    job.Version,
		"status_url": statusURL,
	}, headers)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

// randomFilename generates a random filename with the given extension, using 16 random bytes (32 hex characters).
func randomFilename(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}
