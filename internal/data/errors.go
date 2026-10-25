package data

import "errors"

// ErrRecordNotFound is returned when a requested record does not exist in the database.
var ErrRecordNotFound = errors.New("no record found")

// ErrJobNotActive is returned when an operation is attempted on a job that is not in an active state (e.g., completed or failed).
var ErrJobNotActive = errors.New("job is not in an active state")
