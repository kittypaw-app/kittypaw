package model

import "errors"

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous result")
	// ErrRotationAborted signals that RotateForDevice's old-row revoke
	// matched 0 rows. The handler maps this race-loser path to a silent
	// unauthorized response instead of a generic server error.
	ErrRotationAborted = errors.New("rotation aborted: old refresh not active")
)
