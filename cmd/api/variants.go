package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AlexG-SYS/Test2_ImageProcessing_AlexG_ReeneB/internal/data"
)

var variantNames = map[string]bool{"thumbnail": true, "preview": true, "display": true}

// getVariantHandler retrieves a specific image variant by its name and the associated image's public ID.

func (app *application) getVariantHandler(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !variantNames[name] {
		app.notFoundResponse(w, r)
		return
	}

	img, err := app.models.Images.GetByPublicID(r.PathValue("image_id"))
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(w, r)
			return
		}
		app.serverErrorResponse(w, r, err)
		return
	}

	variant, err := app.models.Variants.GetByImageAndName(img.ID, name)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			// The variant record is missing from the database, which shouldn't happen if the database is consistent.
			app.notFoundResponse(w, r)
			return
		}
		app.serverErrorResponse(w, r, err)
		return
	}

	path := filepath.Join(app.config.storage.variantsDir, variant.StoredFilename)

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The variant file is missing from disk, which shouldn't happen if the database is consistent.
			app.serverErrorResponse(w, r, err)
			return
		}
		app.serverErrorResponse(w, r, err)
		return
	}
	defer f.Close()

	contentType := "image/jpeg"
	if img.MediaType == "image/png" {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, variant.StoredFilename, variant.CreatedAt, f)
}
