package project

import "errors"

var (
	ErrInvalidSystem     = errors.New("invalid systems definition")
	ErrInvalidService    = errors.New("invalid service definition")
	ErrInvalidConnector  = errors.New("invalid connector definition")
	ErrCircularReference = errors.New("circular project reference")
	ErrNotRegistered     = errors.New("project is not registered")
)
