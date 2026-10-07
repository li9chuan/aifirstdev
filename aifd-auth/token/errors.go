package token

import "errors"

var (
	// ErrInvalidToken indicates that a login token is malformed or has an invalid signature.
	ErrInvalidToken = errors.New("invalid token")
	// ErrExpired indicates that a login token is outside its validity window.
	ErrExpired = errors.New("token expired")
	// ErrInvalidPayload indicates that signed payload bytes are missing.
	ErrInvalidPayload = errors.New("invalid token payload")
	// ErrUnknownKey indicates that the token key id is not configured locally.
	ErrUnknownKey = errors.New("unknown token key")
)
