package util

import "fmt"

var (
	ErrIPOutOfCIDR          = fmt.Errorf("IP out of CIDR")
	ErrOffsetOutOfCIDRRange = fmt.Errorf("offset out of CIDR range")
)
