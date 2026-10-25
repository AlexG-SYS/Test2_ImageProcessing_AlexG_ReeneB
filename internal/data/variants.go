package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Variant represents a single generated variant of an original image,
// with metadata about its dimensions, file size, and how it is stored on disk.
type Variant struct {
	ID             string    `json:"-"`
	ImageID        string    `json:"-"`
	Name           string    `json:"name"`
	StoredFilename string    `json:"-"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	SizeBytes      int64     `json:"-"`
	URL            string    `json:"url"`
	CreatedAt      time.Time `json:"-"`
}

type VariantModel struct {
	DB *sql.DB
}

// Insert adds a new variant record to the database for the given image ID and variant name, along with its stored filename, dimensions, and file size.
func (m VariantModel) Insert(imageID, name, storedFilename string, width, height int, sizeBytes int64) error {
	query := `
		INSERT INTO image_variants (image_id, name, stored_filename, width, height, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6)`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := m.DB.ExecContext(ctx, query, imageID, name, storedFilename, width, height, sizeBytes)
	return err
}

// GetByImageAndName retrieves a variant record from the database by its associated image ID and variant name.
// If no record is found, it returns ErrRecordNotFound.
func (m VariantModel) GetByImageAndName(imageID, name string) (*Variant, error) {
	query := `
		SELECT stored_filename, width, height, size_bytes, created_at
		FROM image_variants
		WHERE image_id = $1 AND name = $2`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	v := &Variant{ImageID: imageID, Name: name}
	err := m.DB.QueryRowContext(ctx, query, imageID, name).Scan(
		&v.StoredFilename, &v.Width, &v.Height, &v.SizeBytes, &v.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return v, nil
}

// ListByImageID retrieves all variant records associated with a given image ID, returning them in order by variant name.
func (m VariantModel) ListByImageID(imageInternalID, imagePublicID string) ([]Variant, error) {
	query := `
		SELECT name, width, height
		FROM image_variants
		WHERE image_id = $1
		ORDER BY name`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, imageInternalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var variants []Variant
	for rows.Next() {
		var v Variant
		v.ImageID = imagePublicID
		if err := rows.Scan(&v.Name, &v.Width, &v.Height); err != nil {
			return nil, err
		}
		v.URL = fmt.Sprintf("/v1/images/%s/variants/%s", imagePublicID, v.Name)
		variants = append(variants, v)
	}
	return variants, rows.Err()
}
