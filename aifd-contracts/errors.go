package contracts

import "errors"

var (
	ErrEmptyModuleID   = errors.New("empty module id")
	ErrEmptyModuleKind = errors.New("empty module kind")
)
