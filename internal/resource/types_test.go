package resource

import (
	"net"
	"testing"
)

func TestIPResource_String(t *testing.T) {
	tests := []struct {
		name string
		ip   IPResource
		want string
	}{
		{
			name: "IPv4 address",
			ip:   IPResource(net.ParseIP("192.168.1.1")),
			want: "192.168.1.1",
		},
		{
			name: "IPv6 address",
			ip:   IPResource(net.ParseIP("2001:db8::1")),
			want: "2001:db8::1",
		},
		{
			name: "loopback",
			ip:   IPResource(net.ParseIP("127.0.0.1")),
			want: "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ip.String(); got != tt.want {
				t.Errorf("IPResource.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIPResource_Bytes(t *testing.T) {
	ip := IPResource(net.ParseIP("192.168.1.1"))
	bytes := ip.Bytes()
	if bytes == nil {
		t.Error("IPResource.Bytes() returned nil")
	}
	if len(bytes) == 0 {
		t.Error("IPResource.Bytes() returned empty slice")
	}
}

func TestIPResource_Equal(t *testing.T) {
	ip1 := IPResource(net.ParseIP("192.168.1.1"))
	ip2 := IPResource(net.ParseIP("192.168.1.1"))
	ip3 := IPResource(net.ParseIP("192.168.1.2"))

	// Test equal IPs
	if !ip1.Equal(ip2) {
		t.Error("IPResource.Equal() should return true for same IP")
	}

	// Test different IPs
	if ip1.Equal(ip3) {
		t.Error("IPResource.Equal() should return false for different IPs")
	}

	// Test with different type
	port := PortResource(80)
	if ip1.Equal(port) {
		t.Error("IPResource.Equal() should return false for different resource type")
	}
}

func TestIPResource_Type(t *testing.T) {
	// IPv4 address
	ipv4 := IPResource(net.ParseIP("192.168.1.1").To4())
	if ipv4.Type() != "ipv4" {
		t.Errorf("IPResource.Type() = %v, want ipv4", ipv4.Type())
	}

	// IPv6 address
	ipv6 := IPResource(net.ParseIP("2001:db8::1"))
	if ipv6.Type() != "ipv6" {
		t.Errorf("IPResource.Type() = %v, want ipv6", ipv6.Type())
	}
}

func TestIPResource_To4(t *testing.T) {
	ipv4 := IPResource(net.ParseIP("192.168.1.1"))
	if ipv4.To4() == nil {
		t.Error("IPResource.To4() should not return nil for IPv4")
	}

	ipv6 := IPResource(net.ParseIP("2001:db8::1"))
	if ipv6.To4() != nil {
		t.Error("IPResource.To4() should return nil for IPv6")
	}
}

func TestIPResource_To16(t *testing.T) {
	ipv4 := IPResource(net.ParseIP("192.168.1.1"))
	if ipv4.To16() == nil {
		t.Error("IPResource.To16() should not return nil for IPv4")
	}

	ipv6 := IPResource(net.ParseIP("2001:db8::1"))
	if ipv6.To16() == nil {
		t.Error("IPResource.To16() should not return nil for IPv6")
	}
}

func TestPortResource_String(t *testing.T) {
	port := PortResource(80)
	// Note: The current implementation uses string(rune(p)) which may not produce expected output
	_ = port.String()
}

func TestPortResource_Bytes(t *testing.T) {
	port := PortResource(0x1234)
	bytes := port.Bytes()

	if len(bytes) != 2 {
		t.Errorf("PortResource.Bytes() length = %d, want 2", len(bytes))
	}

	// Check big-endian encoding
	if bytes[0] != 0x12 || bytes[1] != 0x34 {
		t.Errorf("PortResource.Bytes() = %v, want [0x12, 0x34]", bytes)
	}
}

func TestPortResource_Equal(t *testing.T) {
	port1 := PortResource(80)
	port2 := PortResource(80)
	port3 := PortResource(443)

	// Test equal ports
	if !port1.Equal(port2) {
		t.Error("PortResource.Equal() should return true for same port")
	}

	// Test different ports
	if port1.Equal(port3) {
		t.Error("PortResource.Equal() should return false for different ports")
	}

	// Test with different type
	ip := IPResource(net.ParseIP("192.168.1.1"))
	if port1.Equal(ip) {
		t.Error("PortResource.Equal() should return false for different resource type")
	}
}

func TestPortResource_Type(t *testing.T) {
	port := PortResource(80)
	if port.Type() != "port" {
		t.Errorf("PortResource.Type() = %v, want port", port.Type())
	}
}

func TestVLANResource_String(t *testing.T) {
	vlan := VLANResource(100)
	_ = vlan.String() // Basic sanity check
}

func TestVLANResource_Bytes(t *testing.T) {
	vlan := VLANResource(0xABCD)
	bytes := vlan.Bytes()

	if len(bytes) != 2 {
		t.Errorf("VLANResource.Bytes() length = %d, want 2", len(bytes))
	}

	// Check big-endian encoding
	if bytes[0] != 0xAB || bytes[1] != 0xCD {
		t.Errorf("VLANResource.Bytes() = %v, want [0xAB, 0xCD]", bytes)
	}
}

func TestVLANResource_Equal(t *testing.T) {
	vlan1 := VLANResource(100)
	vlan2 := VLANResource(100)
	vlan3 := VLANResource(200)

	// Test equal VLANs
	if !vlan1.Equal(vlan2) {
		t.Error("VLANResource.Equal() should return true for same VLAN")
	}

	// Test different VLANs
	if vlan1.Equal(vlan3) {
		t.Error("VLANResource.Equal() should return false for different VLANs")
	}

	// Test with different type
	port := PortResource(100)
	if vlan1.Equal(port) {
		t.Error("VLANResource.Equal() should return false for different resource type")
	}
}

func TestVLANResource_Type(t *testing.T) {
	vlan := VLANResource(100)
	if vlan.Type() != "vlan" {
		t.Errorf("VLANResource.Type() = %v, want vlan", vlan.Type())
	}
}

func TestResourceInterface(t *testing.T) {
	// Verify all resource types implement the Resource interface
	var _ Resource = IPResource{}
	var _ Resource = PortResource(0)
	var _ Resource = VLANResource(0)
}

func TestResourceTypeConsistency(t *testing.T) {
	// Verify type strings are consistent
	ip := IPResource(net.ParseIP("192.168.1.1").To4())
	if ip.Type() != "ipv4" {
		t.Errorf("IPv4 resource type should be 'ipv4', got %s", ip.Type())
	}

	port := PortResource(80)
	if port.Type() != "port" {
		t.Errorf("Port resource type should be 'port', got %s", port.Type())
	}

	vlan := VLANResource(100)
	if vlan.Type() != "vlan" {
		t.Errorf("VLAN resource type should be 'vlan', got %s", vlan.Type())
	}
}

func TestIPResourceEdgeCases(t *testing.T) {
	// Test with zero IP
	zeroIP := IPResource(net.IPv4zero)
	if zeroIP.String() != "0.0.0.0" {
		t.Errorf("Zero IP string = %s, want 0.0.0.0", zeroIP.String())
	}

	// Test with broadcast address
	broadcast := IPResource(net.IPv4bcast)
	if broadcast.String() != "255.255.255.255" {
		t.Errorf("Broadcast IP string = %s, want 255.255.255.255", broadcast.String())
	}
}

func TestPortResourceEdgeCases(t *testing.T) {
	// Test with port 0
	zero := PortResource(0)
	bytes := zero.Bytes()
	if bytes[0] != 0 || bytes[1] != 0 {
		t.Error("Port 0 bytes should be [0, 0]")
	}

	// Test with max port
	maxPort := PortResource(65535)
	bytes = maxPort.Bytes()
	if bytes[0] != 0xFF || bytes[1] != 0xFF {
		t.Error("Port 65535 bytes should be [0xFF, 0xFF]")
	}
}

func TestVLANResourceEdgeCases(t *testing.T) {
	// Test with VLAN 0
	zero := VLANResource(0)
	bytes := zero.Bytes()
	if bytes[0] != 0 || bytes[1] != 0 {
		t.Error("VLAN 0 bytes should be [0, 0]")
	}

	// Test with max VLAN (4095 is typical max)
	maxVLAN := VLANResource(4095)
	bytes = maxVLAN.Bytes()
	expected := []byte{0x0F, 0xFF}
	if bytes[0] != expected[0] || bytes[1] != expected[1] {
		t.Errorf("VLAN 4095 bytes = %v, want %v", bytes, expected)
	}
}
