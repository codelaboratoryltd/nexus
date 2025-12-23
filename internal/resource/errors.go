package resource

import "errors"

var (
	// ErrPoolExhausted is returned when no more resources are available in the pool
	ErrPoolExhausted = errors.New("pool exhausted: no available resources")

	// ErrResourceNotInPool is returned when trying to operate on a resource not in the pool
	ErrResourceNotInPool = errors.New("resource not in pool")

	// ErrResourceAlreadyAllocated is returned when trying to allocate an already allocated resource
	ErrResourceAlreadyAllocated = errors.New("resource already allocated")

	// ErrResourceNotAllocated is returned when trying to deallocate an unallocated resource
	ErrResourceNotAllocated = errors.New("resource not allocated")

	// ErrInvalidConfig is returned when pool configuration is invalid
	ErrInvalidConfig = errors.New("invalid pool configuration")

	// ErrInvalidRange is returned when a range specification is invalid
	ErrInvalidRange = errors.New("invalid range specification")

	// ErrCannotSplit is returned when a range cannot be split as requested
	ErrCannotSplit = errors.New("cannot split range into requested number of parts")

	// ErrTypeMismatch is returned when resource types don't match
	ErrTypeMismatch = errors.New("resource type mismatch")
)
