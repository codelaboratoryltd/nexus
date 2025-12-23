package hashring

import (
	"errors"
	"fmt"
	"net"
)

var (
	errEmptyCIDR         = fmt.Errorf("CIDR cannot be empty")
	errCIDRNotSplitable  = fmt.Errorf("CIDR cannot be split into the requested number of parts")
	errEmptyPoolID       = fmt.Errorf("pool ID cannot be empty")
	errInvalidSubnetType = fmt.Errorf("invalid subnet type")
	errInvalidPoolCIDR   = fmt.Errorf("invalid pool CIDR")
)

// HashNotFoundError represents an error when a hash is not found in the hash ring
type HashNotFoundError struct {
	HashID HashID
}

// Error implements the error interface for HashNotFoundError
func (e *HashNotFoundError) Error() string {
	return fmt.Sprintf("hash %s does not exist in the hash ring", e.HashID)
}

// Is allows errors.Is to identify HashNotFoundError as a specific error
func (e *HashNotFoundError) Is(target error) bool {
	var hashNotFoundError *HashNotFoundError
	ok := errors.As(target, &hashNotFoundError)
	return ok
}

// CIDRSplitError represents an error when splitting a CIDR fails
type CIDRSplitError struct {
	Network *net.IPNet
	Parts   uint
}

// Error implements the error interface for CIDRSplitError
func (e *CIDRSplitError) Error() string {
	return fmt.Sprintf("cannot split CIDR %s into %d parts", e.Network.String(), e.Parts)
}

// Is allows errors.Is to identify CIDRSplitError as a specific error
func (e *CIDRSplitError) Is(target error) bool {
	var cidrSplitError *CIDRSplitError
	ok := errors.As(target, &cidrSplitError)
	return ok
}

// InvalidPoolError represents an error for invalid pool configuration
type InvalidPoolError struct {
	err    error
	poolID PoolID
}

func (ip InvalidPoolError) Error() string {
	return fmt.Sprintf("invalid pool - %s - %s", ip.poolID, ip.err.Error())
}

func (ip InvalidPoolError) GetPoolID() PoolID {
	return ip.poolID
}
