package shortener

import "errors"

var (
	ErrInvalidURL         = errors.New("invalid URL")
	ErrInvalidCode        = errors.New("invalid short code")
	ErrNotFound           = errors.New("short URL not found")
	ErrStorageUnavailable = errors.New("storage unavailable")
	ErrCodeGeneration     = errors.New("short code generation failed")
	ErrCollisionExhausted = errors.New("short code collision attempts exhausted")
)
