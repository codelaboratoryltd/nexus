package util

import (
	"errors"
	"math/big"
	"net"
	"testing"
)

func TestIPToOffset(t *testing.T) {
	tests := []struct {
		name       string
		cidr       string
		ip         string
		wantOffset int64
		wantErr    bool
	}{
		{
			name:       "first IP in /24",
			cidr:       "192.168.0.0/24",
			ip:         "192.168.0.0",
			wantOffset: 0,
			wantErr:    false,
		},
		{
			name:       "second IP in /24",
			cidr:       "192.168.0.0/24",
			ip:         "192.168.0.1",
			wantOffset: 1,
			wantErr:    false,
		},
		{
			name:       "last IP in /24",
			cidr:       "192.168.0.0/24",
			ip:         "192.168.0.255",
			wantOffset: 255,
			wantErr:    false,
		},
		{
			name:       "middle IP in /24",
			cidr:       "192.168.0.0/24",
			ip:         "192.168.0.100",
			wantOffset: 100,
			wantErr:    false,
		},
		{
			name:       "IP outside CIDR",
			cidr:       "192.168.0.0/24",
			ip:         "192.168.1.0",
			wantOffset: 0,
			wantErr:    true,
		},
		{
			name:       "completely different network",
			cidr:       "192.168.0.0/24",
			ip:         "10.0.0.1",
			wantOffset: 0,
			wantErr:    true,
		},
		{
			name:       "/16 network",
			cidr:       "10.0.0.0/16",
			ip:         "10.0.1.1",
			wantOffset: 257, // 256 + 1
			wantErr:    false,
		},
		{
			name:       "/8 network",
			cidr:       "10.0.0.0/8",
			ip:         "10.1.0.0",
			wantOffset: 65536, // 256 * 256
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cidr, err := net.ParseCIDR(tt.cidr)
			if err != nil {
				t.Fatalf("Failed to parse CIDR: %v", err)
			}

			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("Failed to parse IP: %s", tt.ip)
			}

			offset, err := IPToOffset(cidr, ip)
			if (err != nil) != tt.wantErr {
				t.Errorf("IPToOffset() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if offset.Int64() != tt.wantOffset {
					t.Errorf("IPToOffset() = %d, want %d", offset.Int64(), tt.wantOffset)
				}
			}
		})
	}
}

func TestIPToOffset_ErrorType(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	ip := net.ParseIP("10.0.0.1")

	_, err := IPToOffset(cidr, ip)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !errors.Is(err, ErrIPOutOfCIDR) {
		t.Errorf("Expected ErrIPOutOfCIDR, got %v", err)
	}
}

func TestOffsetToIP(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		offset  int64
		wantIP  string
		wantErr bool
	}{
		{
			name:    "offset 0 in /24",
			cidr:    "192.168.0.0/24",
			offset:  0,
			wantIP:  "192.168.0.0",
			wantErr: false,
		},
		{
			name:    "offset 1 in /24",
			cidr:    "192.168.0.0/24",
			offset:  1,
			wantIP:  "192.168.0.1",
			wantErr: false,
		},
		{
			name:    "offset 255 in /24",
			cidr:    "192.168.0.0/24",
			offset:  255,
			wantIP:  "192.168.0.255",
			wantErr: false,
		},
		{
			name:    "offset 100 in /24",
			cidr:    "192.168.0.0/24",
			offset:  100,
			wantIP:  "192.168.0.100",
			wantErr: false,
		},
		{
			name:    "offset exceeds CIDR range",
			cidr:    "192.168.0.0/24",
			offset:  256, // Max is 255 for /24
			wantIP:  "",
			wantErr: true,
		},
		{
			name:    "large offset exceeds range",
			cidr:    "192.168.0.0/24",
			offset:  1000,
			wantIP:  "",
			wantErr: true,
		},
		{
			name:    "/16 network offset",
			cidr:    "10.0.0.0/16",
			offset:  257,
			wantIP:  "10.0.1.1",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cidr, err := net.ParseCIDR(tt.cidr)
			if err != nil {
				t.Fatalf("Failed to parse CIDR: %v", err)
			}

			offset := big.NewInt(tt.offset)
			ip, err := OffsetToIP(cidr, offset)
			if (err != nil) != tt.wantErr {
				t.Errorf("OffsetToIP() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Compare as strings since IPv4 may be represented as IPv6
				if ip.To4() != nil {
					if ip.To4().String() != tt.wantIP {
						t.Errorf("OffsetToIP() = %s, want %s", ip.To4().String(), tt.wantIP)
					}
				} else {
					t.Errorf("Expected IPv4 result, got IPv6: %s", ip.String())
				}
			}
		})
	}
}

func TestOffsetToIP_ErrorType(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("192.168.0.0/24")
	offset := big.NewInt(1000) // Way too large

	_, err := OffsetToIP(cidr, offset)
	if err == nil {
		t.Fatal("Expected error")
	}
	if !errors.Is(err, ErrOffsetOutOfCIDRRange) {
		t.Errorf("Expected ErrOffsetOutOfCIDRRange, got %v", err)
	}
}

func TestIPToOffsetAndBack(t *testing.T) {
	// Test roundtrip: IP -> Offset -> IP
	testCases := []struct {
		cidr string
		ip   string
	}{
		{"192.168.0.0/24", "192.168.0.0"},
		{"192.168.0.0/24", "192.168.0.1"},
		{"192.168.0.0/24", "192.168.0.100"},
		{"192.168.0.0/24", "192.168.0.255"},
		{"10.0.0.0/16", "10.0.0.1"},
		{"10.0.0.0/16", "10.0.255.255"},
		{"172.16.0.0/12", "172.16.0.1"},
		{"172.16.0.0/12", "172.31.255.254"},
	}

	for _, tc := range testCases {
		t.Run(tc.ip, func(t *testing.T) {
			_, cidr, _ := net.ParseCIDR(tc.cidr)
			originalIP := net.ParseIP(tc.ip)

			// Convert to offset
			offset, err := IPToOffset(cidr, originalIP)
			if err != nil {
				t.Fatalf("IPToOffset failed: %v", err)
			}

			// Convert back to IP
			resultIP, err := OffsetToIP(cidr, offset)
			if err != nil {
				t.Fatalf("OffsetToIP failed: %v", err)
			}

			// Compare
			if !originalIP.To16().Equal(resultIP.To16()) {
				t.Errorf("Roundtrip failed: %s -> %d -> %s", originalIP, offset.Int64(), resultIP)
			}
		})
	}
}

func TestIPToOffset_IPv6(t *testing.T) {
	// Test with IPv6 CIDR
	_, cidr, err := net.ParseCIDR("2001:db8::/32")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	ip := net.ParseIP("2001:db8::1")
	offset, err := IPToOffset(cidr, ip)
	if err != nil {
		t.Errorf("IPToOffset for IPv6 failed: %v", err)
	}
	if offset.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("Expected offset 1, got %s", offset.String())
	}
}

func TestOffsetToIP_IPv6(t *testing.T) {
	_, cidr, err := net.ParseCIDR("2001:db8::/64")
	if err != nil {
		t.Fatalf("Failed to parse CIDR: %v", err)
	}

	offset := big.NewInt(1)
	ip, err := OffsetToIP(cidr, offset)
	if err != nil {
		t.Errorf("OffsetToIP for IPv6 failed: %v", err)
	}

	expected := net.ParseIP("2001:db8::1")
	if !ip.Equal(expected) {
		t.Errorf("OffsetToIP = %s, want %s", ip.String(), expected.String())
	}
}

func TestErrors(t *testing.T) {
	// Test that error variables are properly defined
	if ErrIPOutOfCIDR == nil {
		t.Error("ErrIPOutOfCIDR is nil")
	}
	if ErrOffsetOutOfCIDRRange == nil {
		t.Error("ErrOffsetOutOfCIDRRange is nil")
	}

	// Test error messages
	if ErrIPOutOfCIDR.Error() != "IP out of CIDR" {
		t.Errorf("ErrIPOutOfCIDR message = %q", ErrIPOutOfCIDR.Error())
	}
	if ErrOffsetOutOfCIDRRange.Error() != "offset out of CIDR range" {
		t.Errorf("ErrOffsetOutOfCIDRRange message = %q", ErrOffsetOutOfCIDRRange.Error())
	}
}

func BenchmarkIPToOffset(b *testing.B) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	ip := net.ParseIP("10.128.64.32")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = IPToOffset(cidr, ip)
	}
}

func BenchmarkOffsetToIP(b *testing.B) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	offset := big.NewInt(8421408) // 10.128.64.32

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = OffsetToIP(cidr, offset)
	}
}
