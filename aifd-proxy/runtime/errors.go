package runtime

import "errors"

var (
	ErrConnectionExists   = errors.New("proxy connection already exists")
	ErrConnectionNotFound = errors.New("proxy connection not found")
	ErrInvalidIdentity    = errors.New("proxy identity is invalid")
	ErrUpstreamMissing    = errors.New("proxy upstream is missing")
)
