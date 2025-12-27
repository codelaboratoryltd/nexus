package store

import "fmt"

var (
	ErrInvalidPoolPrefix = fmt.Errorf("invalid pool prefix")
	ErrInvalidPoolCIDR   = fmt.Errorf("invalid pool CIDR")
	ErrMalformedKey      = fmt.Errorf("malformed key")
	ErrNoAllocationFound = fmt.Errorf("no allocation found")
	ErrPoolNotFound      = fmt.Errorf("pool not found")
)
