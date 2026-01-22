package validation

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePoolID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid simple", "pool1", false},
		{"valid with hyphen", "my-pool", false},
		{"valid with underscore", "my_pool", false},
		{"valid with dot", "my.pool", false},
		{"valid complex", "prod-pool-01.us-east", false},
		{"valid single char", "p", false},
		{"valid two chars", "ab", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 200), true},
		{"starts with hyphen", "-pool", true},
		{"ends with hyphen", "pool-", true},
		{"contains spaces", "my pool", true},
		{"contains special char", "pool@1", true},
		{"sql injection attempt", "pool; DROP TABLE--", true},
		{"path traversal", "../etc/passwd", true},
		{"script injection", "<script>alert(1)</script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePoolID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePoolID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSubscriberID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid simple", "subscriber1", false},
		{"valid MAC format", "00:11:22:33:44:55", false},
		{"valid email format", "user@example.com", false},
		{"valid with colon", "sub:123", false},
		{"valid complex", "GPON-01:sub_123@region.1", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 300), true},
		{"contains spaces", "sub scriber", true},
		{"sql injection", "sub'; DROP TABLE--", true},
		{"path traversal", "../../etc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSubscriberID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSubscriberID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCIDR(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{"valid IPv4 /24", "192.168.1.0/24", false},
		{"valid IPv4 /16", "10.0.0.0/16", false},
		{"valid IPv4 /8", "172.0.0.0/8", false},
		{"valid IPv6", "2001:db8::/32", false},
		{"empty", "", true},
		{"invalid format", "not-a-cidr", true},
		{"missing prefix", "192.168.1.0", true},
		{"invalid IP", "999.999.999.999/24", true},
		{"host address not network", "192.168.1.1/24", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateCIDR(tt.cidr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCIDR(%q) error = %v, wantErr %v", tt.cidr, err, tt.wantErr)
			}
		})
	}
}

func TestValidateIPAddress(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
	}{
		{"valid IPv4", "192.168.1.1", false},
		{"valid IPv6", "2001:db8::1", false},
		{"valid IPv4 loopback", "127.0.0.1", false},
		{"valid IPv6 loopback", "::1", false},
		{"empty", "", true},
		{"invalid format", "not-an-ip", true},
		{"out of range", "256.256.256.256", true},
		{"partial IP", "192.168", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateIPAddress(tt.ip)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIPAddress(%q) error = %v, wantErr %v", tt.ip, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMACAddress(t *testing.T) {
	tests := []struct {
		name    string
		mac     string
		wantErr bool
	}{
		{"valid colon format", "00:11:22:33:44:55", false},
		{"valid hyphen format", "00-11-22-33-44-55", false},
		{"valid no separator", "001122334455", false},
		{"valid lowercase", "aa:bb:cc:dd:ee:ff", false},
		{"valid mixed case", "Aa:Bb:Cc:Dd:Ee:Ff", false},
		{"empty", "", true},
		{"invalid format", "not-a-mac", true},
		{"too short", "00:11:22", true},
		{"too long", "00:11:22:33:44:55:66", true},
		{"invalid hex", "GG:HH:II:JJ:KK:LL", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateMACAddress(tt.mac)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMACAddress(%q) error = %v, wantErr %v", tt.mac, err, tt.wantErr)
			}
		})
	}
}

func TestValidateVLANID(t *testing.T) {
	tests := []struct {
		name    string
		vlanID  int
		wantErr bool
	}{
		{"valid minimum", 1, false},
		{"valid maximum", 4094, false},
		{"valid middle", 100, false},
		{"zero", 0, true},
		{"negative", -1, true},
		{"above maximum", 4095, true},
		{"way above maximum", 10000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVLANID(tt.vlanID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateVLANID(%d) error = %v, wantErr %v", tt.vlanID, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  int
		isIPv6  bool
		wantErr bool
	}{
		{"valid IPv4 /24", 24, false, false},
		{"valid IPv4 /32", 32, false, false},
		{"valid IPv4 /8", 8, false, false},
		{"valid IPv6 /64", 64, true, false},
		{"valid IPv6 /128", 128, true, false},
		{"valid IPv6 /16", 16, true, false},
		{"IPv4 too small", 7, false, true},
		{"IPv4 too large", 33, false, true},
		{"IPv6 too small", 15, true, true},
		{"IPv6 too large", 129, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePrefix(tt.prefix, tt.isIPv6)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePrefix(%d, %v) error = %v, wantErr %v", tt.prefix, tt.isIPv6, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSerialNumber(t *testing.T) {
	tests := []struct {
		name    string
		serial  string
		wantErr bool
	}{
		{"valid GPON", "GPON12345678", false},
		{"valid short", "ABCD", false},
		{"valid long", strings.Repeat("A", 32), false},
		{"valid mixed", "ABC123XYZ789", false},
		{"valid lowercase", "gpon1234", false}, // Lowercase is converted to uppercase internally
		{"empty", "", true},
		{"too short", "ABC", true},
		{"too long", strings.Repeat("A", 33), true},
		{"contains special", "GPON-123", true},
		{"sql injection", "GPON'; DROP--", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSerialNumber(tt.serial)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSerialNumber(%q) error = %v, wantErr %v", tt.serial, err, tt.wantErr)
			}
		})
	}
}

func TestValidateShardingFactor(t *testing.T) {
	tests := []struct {
		name    string
		factor  int
		wantErr bool
	}{
		{"valid zero", 0, false},
		{"valid one", 1, false},
		{"valid max", 256, false},
		{"negative", -1, true},
		{"above max", 257, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateShardingFactor(tt.factor)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateShardingFactor(%d) error = %v, wantErr %v", tt.factor, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		wantErr  bool
	}{
		{"nil metadata", nil, false},
		{"empty metadata", map[string]string{}, false},
		{"valid single", map[string]string{"key1": "value1"}, false},
		{"valid multiple", map[string]string{"key1": "value1", "key2": "value2"}, false},
		{"valid with hyphen", map[string]string{"my-key": "value"}, false},
		{"valid with underscore", map[string]string{"my_key": "value"}, false},
		{"empty key", map[string]string{"": "value"}, true},
		{"key too long", map[string]string{strings.Repeat("a", 100): "value"}, true},
		{"value too long", map[string]string{"key": strings.Repeat("a", 600)}, true},
		{"key starts with number", map[string]string{"1key": "value"}, true},
		{"dangerous value", map[string]string{"key": "<script>alert(1)</script>"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetadata(tt.metadata)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMetadata() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateExclusions(t *testing.T) {
	tests := []struct {
		name       string
		exclusions []string
		wantErr    bool
	}{
		{"nil", nil, false},
		{"empty", []string{}, false},
		{"valid IP", []string{"192.168.1.1"}, false},
		{"valid CIDR", []string{"192.168.1.0/24"}, false},
		{"valid mixed", []string{"192.168.1.1", "10.0.0.0/8"}, false},
		{"invalid IP", []string{"not-an-ip"}, true},
		{"invalid CIDR", []string{"192.168.1.0/99"}, true},
		{"too many", make([]string, 101), true},
	}

	// Set up the "too many" test case
	for i := 0; i < 101; i++ {
		tests[len(tests)-1].exclusions[i] = "192.168.1.1"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExclusions(tt.exclusions)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateExclusions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal string", "hello world", "hello world"},
		{"with null byte", "hello\x00world", "helloworld"},
		{"with control chars", "hello\x01\x02world", "helloworld"},
		{"preserves newline", "hello\nworld", "hello\nworld"},
		{"preserves tab", "hello\tworld", "hello\tworld"},
		{"trims spaces", "  hello  ", "hello"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeString(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	err := NewValidationError("test_field", "test_value", "test message", ErrInvalidFormat)

	// Test Error() method
	errStr := err.Error()
	if !strings.Contains(errStr, "test_field") {
		t.Errorf("Error() should contain field name, got: %s", errStr)
	}
	if !strings.Contains(errStr, "test message") {
		t.Errorf("Error() should contain message, got: %s", errStr)
	}

	// Test Unwrap()
	if !errors.Is(err, ErrInvalidFormat) {
		t.Error("Unwrap() should return the underlying error")
	}
}

func TestCheckDangerousInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"normal input", "hello world", false},
		{"SQL SELECT", "SELECT * FROM users", true},
		{"SQL DROP", "DROP TABLE users", true},
		{"SQL comment", "value--comment", true},
		{"SQL semicolon", "value; DELETE FROM x", true},
		{"path traversal dots", "../../../etc/passwd", true},
		{"path traversal encoded", "%2e%2e%2f", true},
		{"script tag", "<script>alert(1)</script>", true},
		{"javascript protocol", "javascript:alert(1)", true},
		{"event handler", "onclick=alert(1)", true},
		{"null byte", "hello\x00world", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkDangerousInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkDangerousInput(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePrefixForCIDR(t *testing.T) {
	ipNet1, _ := ValidateCIDR("10.0.0.0/16")
	ipNet2, _ := ValidateCIDR("2001:db8::/32")

	tests := []struct {
		name    string
		cidr    string
		prefix  int
		wantErr bool
	}{
		{"valid IPv4 prefix larger than mask", "10.0.0.0/16", 24, false},
		{"valid IPv4 prefix equal to mask", "10.0.0.0/16", 16, false},
		{"valid IPv4 max prefix", "10.0.0.0/16", 32, false},
		{"invalid IPv4 prefix smaller than mask", "10.0.0.0/16", 8, true},
		{"valid IPv6 prefix larger than mask", "2001:db8::/32", 64, false},
		{"invalid IPv6 prefix smaller than mask", "2001:db8::/32", 16, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cidr = ipNet1
			if strings.Contains(tt.cidr, ":") {
				cidr = ipNet2
			}
			err := ValidatePrefixForCIDR(cidr, tt.prefix)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePrefixForCIDR() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRequestSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		maxSize int64
		wantErr bool
	}{
		{"under limit", 1000, 2000, false},
		{"at limit", 2000, 2000, false},
		{"over limit", 3000, 2000, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequestSize(tt.size, tt.maxSize)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateRequestSize(%d, %d) error = %v, wantErr %v", tt.size, tt.maxSize, err, tt.wantErr)
			}
		})
	}
}

// Benchmark tests to measure performance overhead
func BenchmarkValidatePoolID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidatePoolID("prod-pool-01.us-east")
	}
}

func BenchmarkValidateCIDR(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidateCIDR("192.168.1.0/24")
	}
}

func BenchmarkValidateMACAddress(b *testing.B) {
	for i := 0; i < b.N; i++ {
		ValidateMACAddress("00:11:22:33:44:55")
	}
}

func BenchmarkCheckDangerousInput(b *testing.B) {
	input := "This is a normal input string without any dangerous characters"
	for i := 0; i < b.N; i++ {
		checkDangerousInput(input)
	}
}
