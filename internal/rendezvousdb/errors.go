package rendezvousdb

import "errors"

// Common errors for rendezvous database implementations.
var (
	ErrClosed = errors.New("database is closed")
)
