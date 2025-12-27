package resource

import (
	"net"
)

// IPResource represents an IP address as a resource.
type IPResource net.IP

// String returns the IP address as a string.
func (ip IPResource) String() string {
	return net.IP(ip).String()
}

// Bytes returns the raw bytes of the IP address.
func (ip IPResource) Bytes() []byte {
	return net.IP(ip)
}

// Equal checks if two IP resources are equal.
func (ip IPResource) Equal(other Resource) bool {
	otherIP, ok := other.(IPResource)
	if !ok {
		return false
	}
	return net.IP(ip).Equal(net.IP(otherIP))
}

// Type returns the resource type identifier.
func (ip IPResource) Type() string {
	if len(ip) == net.IPv6len && ip.To4() == nil {
		return "ipv6"
	}
	return "ipv4"
}

// To4 returns the four-byte representation of the IP address.
func (ip IPResource) To4() net.IP {
	return net.IP(ip).To4()
}

// To16 returns the sixteen-byte representation of the IP address.
func (ip IPResource) To16() net.IP {
	return net.IP(ip).To16()
}

// PortResource represents a port number as a resource.
type PortResource uint16

// String returns the port as a string.
func (p PortResource) String() string {
	return string(rune(p))
}

// Bytes returns the port as bytes.
func (p PortResource) Bytes() []byte {
	return []byte{byte(p >> 8), byte(p)}
}

// Equal checks if two port resources are equal.
func (p PortResource) Equal(other Resource) bool {
	otherPort, ok := other.(PortResource)
	if !ok {
		return false
	}
	return p == otherPort
}

// Type returns the resource type identifier.
func (p PortResource) Type() string {
	return "port"
}

// VLANResource represents a VLAN ID as a resource.
type VLANResource uint16

// String returns the VLAN ID as a string.
func (v VLANResource) String() string {
	return string(rune(v))
}

// Bytes returns the VLAN ID as bytes.
func (v VLANResource) Bytes() []byte {
	return []byte{byte(v >> 8), byte(v)}
}

// Equal checks if two VLAN resources are equal.
func (v VLANResource) Equal(other Resource) bool {
	otherVLAN, ok := other.(VLANResource)
	if !ok {
		return false
	}
	return v == otherVLAN
}

// Type returns the resource type identifier.
func (v VLANResource) Type() string {
	return "vlan"
}
