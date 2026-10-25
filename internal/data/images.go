package data

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Image represents a single uploaded image, with metadata about the original file and how it is stored on disk.
type Image struct {
	ID               string    `json:"-"`
	PublicID         string    `json:"id"`
	OriginalFilename string    `json:"original_filename"`
	StoredFilename   string    `json:"-"` // server-controlled disk name; never exposed to the client
	MediaType        string    `json:"media_type"`
	SizeBytes        int64     `json:"size_bytes"`
	CreatedAt        time.Time `json:"created_at"`
}

type ImageModel struct {
	DB *sql.DB
}

// Insert adds a new image record to the database and populates the ID, PublicID, and CreatedAt fields of the provided Image struct.
func (m ImageModel) Insert(img *Image) error {
	query := `
		INSERT INTO images (original_filename, stored_filename, media_type, size_bytes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, public_id, created_at`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return m.DB.QueryRowContext(ctx, query,
		img.OriginalFilename, img.StoredFilename, img.MediaType, img.SizeBytes,
	).Scan(&img.ID, &img.PublicID, &img.CreatedAt)
}

// GetByPublicID retrieves an image record from the database by its public ID. If no record is found, it returns ErrRecordNotFound.
func (m ImageModel) GetByPublicID(publicID string) (*Image, error) {
	query := `
		SELECT id, public_id, original_filename, stored_filename, media_type, size_bytes, created_at
		FROM images
		WHERE public_id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var img Image
	err := m.DB.QueryRowContext(ctx, query, publicID).Scan(
		&img.ID, &img.PublicID, &img.OriginalFilename, &img.StoredFilename,
		&img.MediaType, &img.SizeBytes, &img.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return &img, nil
}

// GetByID retrieves an image record from the database by its internal ID. If no record is found, it returns ErrRecordNotFound.
func (m ImageModel) GetByID(id string) (*Image, error) {
	query := `
		SELECT id, public_id, original_filename, stored_filename, media_type, size_bytes, created_at
		FROM images
		WHERE id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var img Image
	err := m.DB.QueryRowContext(ctx, query, id).Scan(
		&img.ID, &img.PublicID, &img.OriginalFilename, &img.StoredFilename,
		&img.MediaType, &img.SizeBytes, &img.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return &img, nil
}
