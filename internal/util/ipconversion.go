package util

import (
	"fmt"
	"math/big"
	"net"
)

// IPToOffset calculates the offset of an IP within a CIDR.
func IPToOffset(cidr *net.IPNet, ip net.IP) (*big.Int, error) {
	ip = ip.To16()
	cidrBase := cidr.IP.To16()
	if len(ip) != len(cidrBase) || !cidr.Contains(ip) {
		return nil, fmt.Errorf("IP %s is not within the CIDR %s: %w", ip, cidr, ErrIPOutOfCIDR)
	}
	return new(big.Int).Sub(ipToBigInt(ip), ipToBigInt(cidrBase)), nil
}

// OffsetToIP calculates the IP within a CIDR given an offset.
func OffsetToIP(cidr *net.IPNet, offset *big.Int) (net.IP, error) {
	baseBig := ipToBigInt(cidr.IP)

	// Calculate the upper bound for the CIDR range
	ones, bits := cidr.Mask.Size()
	totalIPs := big.NewInt(1).Lsh(big.NewInt(1), uint(bits-ones))
	upperBound := new(big.Int).Add(baseBig, totalIPs)

	// Add the offset to the base IP
	result := new(big.Int).Add(baseBig, offset)

	// Check if the resulting IP is within the CIDR range
	if result.Cmp(upperBound) >= 0 {
		return nil, fmt.Errorf("offset %s exceeds the CIDR range %s: %w", offset, cidr, ErrOffsetOutOfCIDRRange)
	}
	return bigIntToIP(result, len(cidr.IP.To16())), nil
}

// ipToBigInt converts an IP address to a big.Int.
func ipToBigInt(ip net.IP) *big.Int {
	ip = ip.To16() // Ensure the IP is in 16-byte format for both IPv4 and IPv6
	return new(big.Int).SetBytes(ip)
}

// bigIntToIP converts a big.Int back to an IP address.
func bigIntToIP(i *big.Int, byteLen int) net.IP {
	ipBytes := i.Bytes()
	fullBytes := make([]byte, byteLen)
	if len(ipBytes) > byteLen {
		// Prevent overflow
		ipBytes = ipBytes[len(ipBytes)-byteLen:]
	}
	copy(fullBytes[byteLen-len(ipBytes):], ipBytes)
	return net.IP(fullBytes)
}
