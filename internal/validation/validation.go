// Package validation provides input validation for the Nexus API.
// It includes validators for common types like MAC addresses, IP addresses,
// serial numbers, VLAN IDs, and other parameters to prevent injection attacks
// and ensure data integrity.
package validation

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode"
)

// Error types for validation failures
var (
	ErrEmptyValue         = fmt.Errorf("value cannot be empty")
	ErrInvalidFormat      = fmt.Errorf("invalid format")
	ErrValueTooLong       = fmt.Errorf("value exceeds maximum length")
	ErrValueTooShort      = fmt.Errorf("value is too short")
	ErrInvalidCharacters  = fmt.Errorf("value contains invalid characters")
	ErrOutOfRange         = fmt.Errorf("value is out of valid range")
	ErrInvalidCIDR        = fmt.Errorf("invalid CIDR notation")
	ErrInvalidIP          = fmt.Errorf("invalid IP address")
	ErrInvalidMAC         = fmt.Errorf("invalid MAC address")
	ErrInvalidVLAN        = fmt.Errorf("invalid VLAN ID")
	ErrInvalidPrefix      = fmt.Errorf("invalid prefix length")
	ErrInvalidSerial      = fmt.Errorf("invalid serial number")
	ErrInvalidPoolID      = fmt.Errorf("invalid pool ID")
	ErrInvalidSubscriber  = fmt.Errorf("invalid subscriber ID")
	ErrDangerousInput     = fmt.Errorf("potentially dangerous input detected")
	ErrInvalidBackupRatio = fmt.Errorf("invalid backup ratio")
	ErrInvalidNodeID      = fmt.Errorf("invalid node ID")
)

// ValidationError provides detailed information about validation failures
type ValidationError struct {
	Field   string
	Value   string
	Message string
	Err     error
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("validation error for field '%s': %s", e.Field, e.Message)
	}
	return fmt.Sprintf("validation error: %s", e.Message)
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// NewValidationError creates a new validation error
func NewValidationError(field, value, message string, err error) *ValidationError {
	return &ValidationError{
		Field:   field,
		Value:   value,
		Message: message,
		Err:     err,
	}
}

// Constants for validation limits
const (
	MaxPoolIDLength        = 128
	MaxSubscriberIDLength  = 256
	MaxMetadataKeyLength   = 64
	MaxMetadataValueLength = 512
	MaxExclusionsCount     = 100
	MinVLANID              = 1
	MaxVLANID              = 4094
	MinShardingFactor      = 0
	MaxShardingFactor      = 256
	MinIPv4Prefix          = 8
	MaxIPv4Prefix          = 32
	MinIPv6Prefix          = 16
	MaxIPv6Prefix          = 128
	MinBackupRatio         = 0.0
	MaxBackupRatio         = 1.0
	MaxNodeIDLength        = 128
)

// Precompiled regular expressions for common patterns
var (
	// poolIDPattern allows alphanumeric characters, hyphens, underscores, and dots
	poolIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,126}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

	// subscriberIDPattern allows alphanumeric characters, hyphens, underscores, colons, dots, and @
	subscriberIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@:-]{0,254}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

	// serialNumberPattern matches typical OLT serial numbers
	// Formats: GPON123456, ABCD12345678, etc.
	serialNumberPattern = regexp.MustCompile(`^[A-Z0-9]{4,32}$`)

	// siteIDPattern allows alphanumeric characters, hyphens, underscores
	// Formats: london-1, site_001, datacenter-east-1, etc.
	siteIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

	// metadataKeyPattern allows alphanumeric characters, hyphens, underscores
	metadataKeyPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,62}[a-zA-Z0-9]$|^[a-zA-Z]$`)

	// Patterns to detect potentially dangerous input
	sqlInjectionPattern = regexp.MustCompile(`(?i)(--|;|'|"|\/\*|\*\/|xp_|exec|execute|insert|select|delete|update|drop|alter|create|truncate|union|into|load_file|outfile)`)

	// Path traversal pattern
	pathTraversalPattern = regexp.MustCompile(`(\.\.\/|\.\.\\|%2e%2e%2f|%2e%2e\/|\.\.%2f|%2e%2e%5c)`)

	// Script injection pattern
	scriptInjectionPattern = regexp.MustCompile(`(?i)(<script|javascript:|on\w+\s*=|<iframe|<object|<embed)`)
)

// ValidatePoolID validates a pool identifier
func ValidatePoolID(id string) error {
	if id == "" {
		return NewValidationError("pool_id", id, "pool ID is required", ErrEmptyValue)
	}

	if len(id) > MaxPoolIDLength {
		return NewValidationError("pool_id", id, fmt.Sprintf("pool ID exceeds maximum length of %d", MaxPoolIDLength), ErrValueTooLong)
	}

	if !poolIDPattern.MatchString(id) {
		return NewValidationError("pool_id", id, "pool ID must contain only alphanumeric characters, hyphens, underscores, and dots", ErrInvalidFormat)
	}

	if err := checkDangerousInput(id); err != nil {
		return NewValidationError("pool_id", id, "pool ID contains potentially dangerous characters", err)
	}

	return nil
}

// ValidateSubscriberID validates a subscriber identifier
func ValidateSubscriberID(id string) error {
	if id == "" {
		return NewValidationError("subscriber_id", id, "subscriber ID is required", ErrEmptyValue)
	}

	if len(id) > MaxSubscriberIDLength {
		return NewValidationError("subscriber_id", id, fmt.Sprintf("subscriber ID exceeds maximum length of %d", MaxSubscriberIDLength), ErrValueTooLong)
	}

	if !subscriberIDPattern.MatchString(id) {
		return NewValidationError("subscriber_id", id, "subscriber ID must contain only alphanumeric characters, hyphens, underscores, colons, dots, and @", ErrInvalidFormat)
	}

	if err := checkDangerousInput(id); err != nil {
		return NewValidationError("subscriber_id", id, "subscriber ID contains potentially dangerous characters", err)
	}

	return nil
}

// ValidateCIDR validates a CIDR notation string
func ValidateCIDR(cidr string) (*net.IPNet, error) {
	if cidr == "" {
		return nil, NewValidationError("cidr", cidr, "CIDR is required", ErrEmptyValue)
	}

	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, NewValidationError("cidr", cidr, "invalid CIDR notation", ErrInvalidCIDR)
	}

	// Ensure the IP is the network address
	if !ip.Equal(ipNet.IP) {
		return nil, NewValidationError("cidr", cidr, "CIDR must specify the network address, not a host address", ErrInvalidCIDR)
	}

	return ipNet, nil
}

// ValidateIPAddress validates an IP address string
func ValidateIPAddress(ipStr string) (net.IP, error) {
	if ipStr == "" {
		return nil, NewValidationError("ip", ipStr, "IP address is required", ErrEmptyValue)
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return nil, NewValidationError("ip", ipStr, "invalid IP address format", ErrInvalidIP)
	}

	return ip, nil
}

// ValidateMACAddress validates a MAC address string
func ValidateMACAddress(mac string) (net.HardwareAddr, error) {
	if mac == "" {
		return nil, NewValidationError("mac", mac, "MAC address is required", ErrEmptyValue)
	}

	// Normalize MAC address format
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(mac, "-", ":"), ".", ":"))

	// If no separators, insert colons
	if len(normalized) == 12 && !strings.Contains(normalized, ":") {
		var parts []string
		for i := 0; i < 12; i += 2 {
			parts = append(parts, normalized[i:i+2])
		}
		normalized = strings.Join(parts, ":")
	}

	hwAddr, err := net.ParseMAC(normalized)
	if err != nil {
		return nil, NewValidationError("mac", mac, "invalid MAC address format", ErrInvalidMAC)
	}

	return hwAddr, nil
}

// ValidateVLANID validates a VLAN identifier
func ValidateVLANID(vlanID int) error {
	if vlanID < MinVLANID || vlanID > MaxVLANID {
		return NewValidationError("vlan_id", fmt.Sprintf("%d", vlanID),
			fmt.Sprintf("VLAN ID must be between %d and %d", MinVLANID, MaxVLANID), ErrInvalidVLAN)
	}
	return nil
}

// ValidatePrefix validates an IP prefix length
func ValidatePrefix(prefix int, isIPv6 bool) error {
	var minPrefix, maxPrefix int
	if isIPv6 {
		minPrefix, maxPrefix = MinIPv6Prefix, MaxIPv6Prefix
	} else {
		minPrefix, maxPrefix = MinIPv4Prefix, MaxIPv4Prefix
	}

	if prefix < minPrefix || prefix > maxPrefix {
		return NewValidationError("prefix", fmt.Sprintf("%d", prefix),
			fmt.Sprintf("prefix length must be between %d and %d", minPrefix, maxPrefix), ErrInvalidPrefix)
	}
	return nil
}

// ValidatePrefixForCIDR validates that the prefix is valid for the given CIDR
func ValidatePrefixForCIDR(cidr *net.IPNet, prefix int) error {
	maskSize, bits := cidr.Mask.Size()

	// Check if it's IPv6
	isIPv6 := bits == 128

	// Validate the prefix itself
	if err := ValidatePrefix(prefix, isIPv6); err != nil {
		return err
	}

	// Prefix must be >= CIDR mask size (can't allocate larger than the pool)
	if prefix < maskSize {
		return NewValidationError("prefix", fmt.Sprintf("%d", prefix),
			fmt.Sprintf("prefix length must be >= %d (the pool's mask size)", maskSize), ErrInvalidPrefix)
	}

	return nil
}

// ValidateSerialNumber validates an OLT serial number
func ValidateSerialNumber(serial string) error {
	if serial == "" {
		return NewValidationError("serial_number", serial, "serial number is required", ErrEmptyValue)
	}

	// Convert to uppercase for validation
	upperSerial := strings.ToUpper(serial)

	if !serialNumberPattern.MatchString(upperSerial) {
		return NewValidationError("serial_number", serial, "serial number must be 4-32 alphanumeric characters", ErrInvalidSerial)
	}

	if err := checkDangerousInput(serial); err != nil {
		return NewValidationError("serial_number", serial, "serial number contains potentially dangerous characters", err)
	}

	return nil
}

// ValidateSiteID validates a site identifier
func ValidateSiteID(siteID string) error {
	if siteID == "" {
		return NewValidationError("site_id", siteID, "site ID is required", ErrEmptyValue)
	}

	if len(siteID) > 64 {
		return NewValidationError("site_id", siteID, "site ID exceeds maximum length of 64", ErrValueTooLong)
	}

	if !siteIDPattern.MatchString(siteID) {
		return NewValidationError("site_id", siteID, "site ID must contain only alphanumeric characters, hyphens, and underscores", ErrInvalidFormat)
	}

	if err := checkDangerousInput(siteID); err != nil {
		return NewValidationError("site_id", siteID, "site ID contains potentially dangerous characters", err)
	}

	return nil
}

// ValidateShardingFactor validates a sharding factor value
func ValidateShardingFactor(factor int) error {
	if factor < MinShardingFactor || factor > MaxShardingFactor {
		return NewValidationError("sharding_factor", fmt.Sprintf("%d", factor),
			fmt.Sprintf("sharding factor must be between %d and %d", MinShardingFactor, MaxShardingFactor), ErrOutOfRange)
	}
	return nil
}

// ValidateMetadata validates a metadata map
func ValidateMetadata(metadata map[string]string) error {
	if metadata == nil {
		return nil // nil metadata is valid (no metadata)
	}

	for key, value := range metadata {
		// Validate key
		if len(key) == 0 {
			return NewValidationError("metadata_key", key, "metadata key cannot be empty", ErrEmptyValue)
		}
		if len(key) > MaxMetadataKeyLength {
			return NewValidationError("metadata_key", key,
				fmt.Sprintf("metadata key exceeds maximum length of %d", MaxMetadataKeyLength), ErrValueTooLong)
		}
		if !metadataKeyPattern.MatchString(key) {
			return NewValidationError("metadata_key", key,
				"metadata key must start with a letter and contain only alphanumeric characters, hyphens, and underscores", ErrInvalidFormat)
		}

		// Validate value
		if len(value) > MaxMetadataValueLength {
			return NewValidationError("metadata_value", value,
				fmt.Sprintf("metadata value for key '%s' exceeds maximum length of %d", key, MaxMetadataValueLength), ErrValueTooLong)
		}

		// Check for dangerous content
		if err := checkDangerousInput(value); err != nil {
			return NewValidationError("metadata_value", value,
				fmt.Sprintf("metadata value for key '%s' contains potentially dangerous characters", key), err)
		}
	}

	return nil
}

// ValidateExclusions validates a list of IP exclusions
func ValidateExclusions(exclusions []string) error {
	if len(exclusions) > MaxExclusionsCount {
		return NewValidationError("exclusions", "",
			fmt.Sprintf("number of exclusions exceeds maximum of %d", MaxExclusionsCount), ErrOutOfRange)
	}

	for i, exclusion := range exclusions {
		// Try parsing as CIDR first
		if strings.Contains(exclusion, "/") {
			if _, err := ValidateCIDR(exclusion); err != nil {
				return NewValidationError("exclusions", exclusion,
					fmt.Sprintf("exclusion at index %d is not a valid CIDR", i), ErrInvalidCIDR)
			}
		} else {
			// Try parsing as IP address
			if _, err := ValidateIPAddress(exclusion); err != nil {
				return NewValidationError("exclusions", exclusion,
					fmt.Sprintf("exclusion at index %d is not a valid IP address or CIDR", i), ErrInvalidIP)
			}
		}
	}

	return nil
}

// SanitizeString removes or escapes potentially dangerous characters from a string
func SanitizeString(s string) string {
	// Remove null bytes
	s = strings.ReplaceAll(s, "\x00", "")

	// Remove control characters except newlines and tabs
	var result strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			continue
		}
		result.WriteRune(r)
	}

	return strings.TrimSpace(result.String())
}

// checkDangerousInput checks for potentially dangerous input patterns
func checkDangerousInput(s string) error {
	// Check for SQL injection patterns
	if sqlInjectionPattern.MatchString(s) {
		return ErrDangerousInput
	}

	// Check for path traversal
	if pathTraversalPattern.MatchString(s) {
		return ErrDangerousInput
	}

	// Check for script injection
	if scriptInjectionPattern.MatchString(s) {
		return ErrDangerousInput
	}

	// Check for null bytes
	if strings.Contains(s, "\x00") {
		return ErrDangerousInput
	}

	return nil
}

// ValidateRequestSize validates that a request body is within acceptable limits
func ValidateRequestSize(size int64, maxSize int64) error {
	if size > maxSize {
		return NewValidationError("request_body", fmt.Sprintf("%d bytes", size),
			fmt.Sprintf("request body exceeds maximum size of %d bytes", maxSize), ErrOutOfRange)
	}
	return nil
}

// ValidateBackupRatio validates a backup ratio value (0.0 to 1.0)
func ValidateBackupRatio(ratio float64) error {
	if ratio < MinBackupRatio || ratio > MaxBackupRatio {
		return NewValidationError("backup_ratio", fmt.Sprintf("%f", ratio),
			fmt.Sprintf("backup ratio must be between %f and %f", MinBackupRatio, MaxBackupRatio), ErrInvalidBackupRatio)
	}
	return nil
}

// ValidateNodeID validates a node identifier
func ValidateNodeID(id string) error {
	if id == "" {
		return NewValidationError("node_id", id, "node ID is required", ErrEmptyValue)
	}

	if len(id) > MaxNodeIDLength {
		return NewValidationError("node_id", id, fmt.Sprintf("node ID exceeds maximum length of %d", MaxNodeIDLength), ErrValueTooLong)
	}

	// Use same pattern as pool ID - alphanumeric characters, hyphens, underscores, and dots
	if !poolIDPattern.MatchString(id) {
		return NewValidationError("node_id", id, "node ID must contain only alphanumeric characters, hyphens, underscores, and dots", ErrInvalidFormat)
	}

	if err := checkDangerousInput(id); err != nil {
		return NewValidationError("node_id", id, "node ID contains potentially dangerous characters", err)
	}

	return nil
}
