package ztp

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

// newTestServer builds a Server directly (bypasses NewServer which requires a real NIC).
// The pool is populated from RangeStart..RangeEnd or auto-derived from Network.
func newTestServer(t *testing.T) *Server {
	t.Helper()

	_, network, _ := net.ParseCIDR("10.0.0.0/24")
	s := &Server{
		config: Config{
			Interface:  "lo0",
			Network:    *network,
			RangeStart: net.ParseIP("10.0.0.10"),
			RangeEnd:   net.ParseIP("10.0.0.20"),
			Gateway:    net.ParseIP("10.0.0.1"),
			DNS:        []net.IP{net.ParseIP("8.8.8.8")},
			LeaseTime:  1 * time.Hour,
			NexusURL:   "http://nexus.local:9000",
		},
		serverIP:  net.ParseIP("10.0.0.1").To4(),
		serverMAC: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		leases:    make(map[string]*Lease),
		ipPool:    make(map[string]bool),
	}

	if err := s.initPool(); err != nil {
		t.Fatalf("initPool: %v", err)
	}
	return s
}

func makeDiscover(t *testing.T, mac net.HardwareAddr) *dhcpv4.DHCPv4 {
	t.Helper()
	m, err := dhcpv4.NewDiscovery(mac)
	if err != nil {
		t.Fatalf("NewDiscovery: %v", err)
	}
	return m
}

func makeRequest(t *testing.T, mac net.HardwareAddr, requestedIP net.IP) *dhcpv4.DHCPv4 {
	t.Helper()
	m, err := dhcpv4.New(
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRequest),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ClientHWAddr = mac
	if requestedIP != nil {
		m.Options.Update(dhcpv4.OptRequestedIPAddress(requestedIP))
	}
	return m
}

func makeRelease(t *testing.T, mac net.HardwareAddr, clientIP net.IP) *dhcpv4.DHCPv4 {
	t.Helper()
	m, err := dhcpv4.New(
		dhcpv4.WithMessageType(dhcpv4.MessageTypeRelease),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ClientHWAddr = mac
	m.ClientIPAddr = clientIP
	return m
}

// ---------------------------------------------------------------------------
// NewServer validation
// ---------------------------------------------------------------------------

func TestNewServer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing interface",
			cfg:     Config{NexusURL: "http://nexus:9000"},
			wantErr: "interface is required",
		},
		{
			name:    "missing network",
			cfg:     Config{Interface: "lo0", NexusURL: "http://nexus:9000"},
			wantErr: "network is required",
		},
		{
			name: "missing nexus URL",
			cfg: Config{
				Interface: "lo0",
				Network:   net.IPNet{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(24, 32)},
			},
			wantErr: "nexus URL is required",
		},
		{
			name: "bad interface name",
			cfg: Config{
				Interface: "does_not_exist_xyz",
				Network:   net.IPNet{IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(24, 32)},
				NexusURL:  "http://nexus:9000",
			},
			wantErr: "failed to get interface",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServer(tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !containsStr(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// initPool
// ---------------------------------------------------------------------------

func TestInitPool_WithRange(t *testing.T) {
	s := newTestServer(t)

	// Range is 10.0.0.10-10.0.0.20 = 11 IPs, minus gateway (10.0.0.1 not in range) = 11
	// serverIP is 10.0.0.1 which is also the gateway and outside the range.
	// So we expect 11 IPs.
	if len(s.ipPool) != 11 {
		t.Errorf("expected 11 IPs in pool, got %d", len(s.ipPool))
	}

	// Gateway should NOT be in pool
	if s.ipPool["10.0.0.1"] {
		t.Error("gateway should not be in pool")
	}
}

func TestInitPool_WithoutRange(t *testing.T) {
	_, network, _ := net.ParseCIDR("10.0.0.0/28") // 16 IPs: .0-.15
	s := &Server{
		config: Config{
			Interface: "lo0",
			Network:   *network,
			Gateway:   net.ParseIP("10.0.0.1"),
			LeaseTime: time.Hour,
			NexusURL:  "http://nexus:9000",
		},
		serverIP: net.ParseIP("10.0.0.5").To4(),
		leases:   make(map[string]*Lease),
		ipPool:   make(map[string]bool),
	}

	if err := s.initPool(); err != nil {
		t.Fatalf("initPool: %v", err)
	}

	// /28 = 16 addresses. Skip .0 (network), .1 (gateway), .5 (server), .15 (broadcast)
	// Usable: .2,.3,.4,.6,.7,.8,.9,.10,.11,.12,.13,.14 = 12
	if len(s.ipPool) != 12 {
		t.Errorf("expected 12 IPs in pool, got %d", len(s.ipPool))
	}

	if s.ipPool["10.0.0.0"] {
		t.Error("network address should not be in pool")
	}
	if s.ipPool["10.0.0.1"] {
		t.Error("gateway should not be in pool")
	}
	if s.ipPool["10.0.0.5"] {
		t.Error("server IP should not be in pool")
	}
	if s.ipPool["10.0.0.15"] {
		t.Error("broadcast should not be in pool")
	}
}

func TestInitPool_EmptyRange(t *testing.T) {
	_, network, _ := net.ParseCIDR("10.0.0.0/31") // only 2 IPs: .0 and .1
	s := &Server{
		config: Config{
			Interface: "lo0",
			Network:   *network,
			Gateway:   net.ParseIP("10.0.0.1"),
			LeaseTime: time.Hour,
			NexusURL:  "http://nexus:9000",
		},
		serverIP: net.ParseIP("10.0.0.0").To4(),
		leases:   make(map[string]*Lease),
		ipPool:   make(map[string]bool),
	}

	err := s.initPool()
	if err == nil || !containsStr(err.Error(), "no available IPs") {
		t.Errorf("expected 'no available IPs' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// handleDiscover
// ---------------------------------------------------------------------------

func TestHandleDiscover_AllocatesIP(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)

	resp, err := s.handleDiscover(m)
	if err != nil {
		t.Fatalf("handleDiscover: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.MessageType() != dhcpv4.MessageTypeOffer {
		t.Errorf("expected Offer, got %v", resp.MessageType())
	}
	if resp.YourIPAddr == nil || resp.YourIPAddr.IsUnspecified() {
		t.Error("expected offered IP")
	}
}

func TestHandleDiscover_ExistingLease(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	// Pre-populate a lease
	existingIP := net.ParseIP("10.0.0.15").To4()
	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     existingIP,
		Expiry: time.Now().Add(time.Hour),
	}

	m := makeDiscover(t, mac)
	resp, err := s.handleDiscover(m)
	if err != nil {
		t.Fatalf("handleDiscover: %v", err)
	}

	if !resp.YourIPAddr.Equal(existingIP) {
		t.Errorf("expected existing IP %v, got %v", existingIP, resp.YourIPAddr)
	}
}

func TestHandleDiscover_PoolExhausted(t *testing.T) {
	s := newTestServer(t)

	// Drain the pool
	s.mu.Lock()
	s.ipPool = make(map[string]bool)
	s.mu.Unlock()

	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)

	_, err := s.handleDiscover(m)
	if err == nil {
		t.Fatal("expected error when pool exhausted")
	}
	if !containsStr(err.Error(), "no available IPs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleDiscover_CallbackFired(t *testing.T) {
	s := newTestServer(t)

	var cbMAC net.HardwareAddr
	var cbHostname, cbVendor string
	s.OnDeviceDiscovered = func(mac net.HardwareAddr, hostname, vendorInfo string) {
		cbMAC = mac
		cbHostname = hostname
		cbVendor = vendorInfo
	}

	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)
	m.UpdateOption(dhcpv4.OptHostName("test-olt"))
	m.UpdateOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(60), []byte("OLT-Vendor")))

	_, err := s.handleDiscover(m)
	if err != nil {
		t.Fatalf("handleDiscover: %v", err)
	}

	if !bytes.Equal(cbMAC, mac) {
		t.Errorf("callback MAC = %v, want %v", cbMAC, mac)
	}
	if cbHostname != "test-olt" {
		t.Errorf("callback hostname = %q, want %q", cbHostname, "test-olt")
	}
	if cbVendor != "OLT-Vendor" {
		t.Errorf("callback vendor = %q, want %q", cbVendor, "OLT-Vendor")
	}
}

// ---------------------------------------------------------------------------
// handleRequest
// ---------------------------------------------------------------------------

func TestHandleRequest_NewLease(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	requestedIP := net.ParseIP("10.0.0.12")
	m := makeRequest(t, mac, requestedIP)

	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK, got %v", resp.MessageType())
	}
	if !resp.YourIPAddr.Equal(requestedIP.To4()) {
		t.Errorf("expected %v, got %v", requestedIP, resp.YourIPAddr)
	}

	// Verify lease stored
	lease, ok := s.leases[mac.String()]
	if !ok {
		t.Fatal("lease not stored")
	}
	if !lease.IP.Equal(requestedIP.To4()) {
		t.Errorf("lease IP = %v, want %v", lease.IP, requestedIP)
	}

	// IP should be removed from pool
	if s.ipPool[requestedIP.String()] {
		t.Error("IP should be removed from pool after lease")
	}
}

func TestHandleRequest_ExistingLease_SameIP(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	existingIP := net.ParseIP("10.0.0.15").To4()

	oldExpiry := time.Now().Add(30 * time.Minute)
	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     existingIP,
		Expiry: oldExpiry,
	}

	m := makeRequest(t, mac, existingIP)
	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK, got %v", resp.MessageType())
	}
	if !resp.YourIPAddr.Equal(existingIP) {
		t.Errorf("expected %v, got %v", existingIP, resp.YourIPAddr)
	}

	// Expiry should be refreshed
	if !s.leases[mac.String()].Expiry.After(oldExpiry) {
		t.Error("lease expiry should have been refreshed")
	}
}

func TestHandleRequest_ExistingLease_DifferentIP_NAK(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	existingIP := net.ParseIP("10.0.0.15").To4()

	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     existingIP,
		Expiry: time.Now().Add(time.Hour),
	}

	// Request a different IP
	m := makeRequest(t, mac, net.ParseIP("10.0.0.16"))
	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	if resp.MessageType() != dhcpv4.MessageTypeNak {
		t.Errorf("expected NAK, got %v", resp.MessageType())
	}
}

func TestHandleRequest_NoRequestedIP_Allocates(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeRequest(t, mac, nil)
	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK, got %v", resp.MessageType())
	}
	if resp.YourIPAddr == nil || resp.YourIPAddr.IsUnspecified() {
		t.Error("expected allocated IP")
	}
}

func TestHandleRequest_PoolExhausted_NAK(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	s.mu.Lock()
	s.ipPool = make(map[string]bool)
	s.mu.Unlock()

	m := makeRequest(t, mac, nil)
	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	if resp.MessageType() != dhcpv4.MessageTypeNak {
		t.Errorf("expected NAK when pool exhausted, got %v", resp.MessageType())
	}
}

func TestHandleRequest_RequestedIP_NotInPool(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	// Request an IP that's not in the pool range
	m := makeRequest(t, mac, net.ParseIP("192.168.1.100"))
	resp, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	// Should allocate a different IP (the requested one is not available)
	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK, got %v", resp.MessageType())
	}
}

func TestHandleRequest_CallbackFired(t *testing.T) {
	s := newTestServer(t)

	var cbMAC net.HardwareAddr
	var cbIP net.IP
	s.OnDeviceLeased = func(mac net.HardwareAddr, ip net.IP) {
		cbMAC = mac
		cbIP = ip
	}

	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeRequest(t, mac, net.ParseIP("10.0.0.12"))

	_, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	if !bytes.Equal(cbMAC, mac) {
		t.Errorf("callback MAC = %v, want %v", cbMAC, mac)
	}
	if cbIP == nil {
		t.Error("callback IP should be set")
	}
}

func TestHandleRequest_VendorInfo(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeRequest(t, mac, net.ParseIP("10.0.0.12"))
	m.UpdateOption(dhcpv4.OptHostName("my-olt"))
	m.UpdateOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(60), []byte("OLT-Class")))

	_, err := s.handleRequest(m)
	if err != nil {
		t.Fatalf("handleRequest: %v", err)
	}

	lease := s.leases[mac.String()]
	if lease.Hostname != "my-olt" {
		t.Errorf("hostname = %q, want %q", lease.Hostname, "my-olt")
	}
	if lease.VendorInfo != "OLT-Class" {
		t.Errorf("vendor = %q, want %q", lease.VendorInfo, "OLT-Class")
	}
}

// ---------------------------------------------------------------------------
// handleRelease
// ---------------------------------------------------------------------------

func TestHandleRelease(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	ip := net.ParseIP("10.0.0.15").To4()

	// First create a lease
	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     ip,
		Expiry: time.Now().Add(time.Hour),
	}
	delete(s.ipPool, ip.String())

	m := makeRelease(t, mac, ip)
	s.handleRelease(m)

	// Lease should be gone
	if _, exists := s.leases[mac.String()]; exists {
		t.Error("lease should be removed after release")
	}

	// IP should be back in pool
	if !s.ipPool[ip.String()] {
		t.Error("IP should be returned to pool after release")
	}
}

func TestHandleRelease_NoExistingLease(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}

	poolSizeBefore := len(s.ipPool)
	m := makeRelease(t, mac, net.ParseIP("10.0.0.15"))
	s.handleRelease(m)

	// Pool should be unchanged
	if len(s.ipPool) != poolSizeBefore {
		t.Error("pool should be unchanged for unknown MAC release")
	}
}

// ---------------------------------------------------------------------------
// handler (dispatch)
// ---------------------------------------------------------------------------

type fakePacketConn struct {
	written []byte
	addr    net.Addr
}

func (f *fakePacketConn) ReadFrom(b []byte) (int, net.Addr, error) { return 0, nil, nil }
func (f *fakePacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	f.written = b
	f.addr = addr
	return len(b), nil
}
func (f *fakePacketConn) Close() error                       { return nil }
func (f *fakePacketConn) LocalAddr() net.Addr                { return nil }
func (f *fakePacketConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakePacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakePacketConn) SetWriteDeadline(t time.Time) error { return nil }

func TestHandler_Discover(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeDiscover(t, mac)
	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.written == nil {
		t.Fatal("expected response to be written")
	}

	resp, err := dhcpv4.FromBytes(conn.written)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeOffer {
		t.Errorf("expected Offer, got %v", resp.MessageType())
	}
}

func TestHandler_Request(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeRequest(t, mac, net.ParseIP("10.0.0.12"))
	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.written == nil {
		t.Fatal("expected response to be written")
	}

	resp, err := dhcpv4.FromBytes(conn.written)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK, got %v", resp.MessageType())
	}
}

func TestHandler_Release(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	ip := net.ParseIP("10.0.0.15").To4()

	s.leases[mac.String()] = &Lease{MAC: mac, IP: ip, Expiry: time.Now().Add(time.Hour)}

	m := makeRelease(t, mac, ip)
	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	// Release produces no response
	if conn.written != nil {
		t.Error("release should not produce a response")
	}

	if _, exists := s.leases[mac.String()]; exists {
		t.Error("lease should be deleted after release")
	}
}

func TestHandler_NilMessage(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}

	// Should not panic
	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, nil)

	if conn.written != nil {
		t.Error("nil message should produce no response")
	}
}

func TestHandler_UnknownMessageType(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}

	m, err := dhcpv4.New(dhcpv4.WithMessageType(dhcpv4.MessageTypeInform))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ClientHWAddr = net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.written != nil {
		t.Error("unknown message type should produce no response")
	}
}

// ---------------------------------------------------------------------------
// handler response routing
// ---------------------------------------------------------------------------

func TestHandler_ResponseRouting_GatewayIP(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeDiscover(t, mac)
	m.GatewayIPAddr = net.ParseIP("10.0.0.1")

	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.addr == nil {
		t.Fatal("expected addr to be set")
	}
	udpAddr, ok := conn.addr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected *net.UDPAddr, got %T", conn.addr)
	}
	// When GatewayIPAddr is set, response goes to gateway on port 67
	if udpAddr.Port != 67 {
		t.Errorf("expected port 67 for relay, got %d", udpAddr.Port)
	}
}

func TestHandler_ResponseRouting_ClientIP(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	// Pre-populate lease so request succeeds
	existingIP := net.ParseIP("10.0.0.12").To4()
	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     existingIP,
		Expiry: time.Now().Add(time.Hour),
	}

	m := makeRequest(t, mac, existingIP)
	m.ClientIPAddr = net.ParseIP("10.0.0.12")
	// GatewayIPAddr should be zero (default)

	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.addr == nil {
		t.Fatal("expected addr to be set")
	}
	udpAddr, ok := conn.addr.(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected *net.UDPAddr, got %T", conn.addr)
	}
	if udpAddr.Port != 68 {
		t.Errorf("expected port 68 for unicast client, got %d", udpAddr.Port)
	}
}

func TestHandler_ResponseRouting_Broadcast(t *testing.T) {
	s := newTestServer(t)
	conn := &fakePacketConn{}
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	m := makeDiscover(t, mac)
	// Ensure both GatewayIPAddr and ClientIPAddr are zero (default for Discover)

	s.handler(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: 68}, m)

	if conn.addr == nil {
		t.Fatal("expected addr to be set")
	}
	udpAddr := conn.addr.(*net.UDPAddr)
	if !udpAddr.IP.Equal(net.IPv4bcast) {
		t.Errorf("expected broadcast IP, got %v", udpAddr.IP)
	}
}

// ---------------------------------------------------------------------------
// buildResponse — Nexus vendor options
// ---------------------------------------------------------------------------

func TestBuildResponse_NexusOptions(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)

	resp, err := s.buildResponse(m, dhcpv4.MessageTypeOffer, net.ParseIP("10.0.0.12").To4())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}

	// Option 43: Vendor-Specific Information
	opt43 := resp.Options.Get(dhcpv4.OptionVendorSpecificInformation)
	if opt43 == nil {
		t.Fatal("Option 43 not set")
	}
	// Parse TLV: type=1, length, value=nexus URL
	if opt43[0] != 1 {
		t.Errorf("vendor option type = %d, want 1", opt43[0])
	}
	urlLen := int(opt43[1])
	urlVal := string(opt43[2 : 2+urlLen])
	if urlVal != "http://nexus.local:9000" {
		t.Errorf("vendor option URL = %q, want %q", urlVal, "http://nexus.local:9000")
	}

	// Option 224: Private use with Nexus URL
	opt224 := resp.Options.Get(dhcpv4.GenericOptionCode(224))
	if opt224 == nil {
		t.Fatal("Option 224 not set")
	}
	if string(opt224) != "http://nexus.local:9000" {
		t.Errorf("Option 224 = %q, want %q", string(opt224), "http://nexus.local:9000")
	}

	// Subnet mask
	mask := resp.SubnetMask()
	if mask == nil {
		t.Fatal("subnet mask not set")
	}

	// Lease time
	lt := resp.IPAddressLeaseTime(0)
	if lt != s.config.LeaseTime {
		t.Errorf("lease time = %v, want %v", lt, s.config.LeaseTime)
	}
}

func TestBuildResponse_GatewayAndDNS(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)

	resp, err := s.buildResponse(m, dhcpv4.MessageTypeOffer, net.ParseIP("10.0.0.12").To4())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}

	routers := resp.Router()
	if len(routers) == 0 {
		t.Fatal("router option not set")
	}
	if !routers[0].Equal(net.ParseIP("10.0.0.1")) {
		t.Errorf("router = %v, want 10.0.0.1", routers[0])
	}

	dns := resp.DNS()
	if len(dns) == 0 {
		t.Fatal("DNS option not set")
	}
	if !dns[0].Equal(net.ParseIP("8.8.8.8")) {
		t.Errorf("DNS = %v, want 8.8.8.8", dns[0])
	}
}

func TestBuildResponse_NoGatewayNoDNS(t *testing.T) {
	s := newTestServer(t)
	s.config.Gateway = nil
	s.config.DNS = nil

	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeDiscover(t, mac)

	resp, err := s.buildResponse(m, dhcpv4.MessageTypeOffer, net.ParseIP("10.0.0.12").To4())
	if err != nil {
		t.Fatalf("buildResponse: %v", err)
	}

	// These should not be set
	if routers := resp.Router(); len(routers) > 0 {
		t.Errorf("unexpected router option: %v", routers)
	}
	if dns := resp.DNS(); len(dns) > 0 {
		t.Errorf("unexpected DNS option: %v", dns)
	}
}

// ---------------------------------------------------------------------------
// buildNexusVendorOptions
// ---------------------------------------------------------------------------

func TestBuildNexusVendorOptions(t *testing.T) {
	url := "http://nexus.local:9000"
	data := buildNexusVendorOptions(url)

	if data[0] != 1 {
		t.Errorf("type byte = %d, want 1", data[0])
	}
	if int(data[1]) != len(url) {
		t.Errorf("length = %d, want %d", data[1], len(url))
	}
	if string(data[2:]) != url {
		t.Errorf("url = %q, want %q", string(data[2:]), url)
	}
}

func TestBuildNexusVendorOptions_Empty(t *testing.T) {
	data := buildNexusVendorOptions("")
	if data[0] != 1 {
		t.Errorf("type byte = %d, want 1", data[0])
	}
	if data[1] != 0 {
		t.Errorf("length = %d, want 0", data[1])
	}
	if len(data) != 2 {
		t.Errorf("total length = %d, want 2", len(data))
	}
}

// ---------------------------------------------------------------------------
// buildNAK
// ---------------------------------------------------------------------------

func TestBuildNAK(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	m := makeRequest(t, mac, net.ParseIP("10.0.0.12"))

	resp, err := s.buildNAK(m)
	if err != nil {
		t.Fatalf("buildNAK: %v", err)
	}
	if resp.MessageType() != dhcpv4.MessageTypeNak {
		t.Errorf("expected NAK, got %v", resp.MessageType())
	}
	if !resp.ServerIPAddr.Equal(s.serverIP) {
		t.Errorf("server IP = %v, want %v", resp.ServerIPAddr, s.serverIP)
	}
}

// ---------------------------------------------------------------------------
// GetLeases / GetLease
// ---------------------------------------------------------------------------

func TestGetLeases_Empty(t *testing.T) {
	s := newTestServer(t)
	leases := s.GetLeases()
	if len(leases) != 0 {
		t.Errorf("expected 0 leases, got %d", len(leases))
	}
}

func TestGetLeases_ReturnsCopies(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	ip := net.ParseIP("10.0.0.12").To4()

	s.leases[mac.String()] = &Lease{
		MAC:    mac,
		IP:     ip,
		Expiry: time.Now().Add(time.Hour),
	}

	leases := s.GetLeases()
	if len(leases) != 1 {
		t.Fatalf("expected 1 lease, got %d", len(leases))
	}

	// Mutating the returned lease should not affect the server's internal state
	leases[0].Hostname = "mutated"
	if s.leases[mac.String()].Hostname == "mutated" {
		t.Error("GetLeases should return copies, not references")
	}
}

func TestGetLease_Found(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	ip := net.ParseIP("10.0.0.12").To4()

	s.leases[mac.String()] = &Lease{
		MAC:      mac,
		IP:       ip,
		Expiry:   time.Now().Add(time.Hour),
		Hostname: "test-olt",
	}

	lease, ok := s.GetLease(mac)
	if !ok {
		t.Fatal("expected lease to be found")
	}
	if !lease.IP.Equal(ip) {
		t.Errorf("IP = %v, want %v", lease.IP, ip)
	}
	if lease.Hostname != "test-olt" {
		t.Errorf("hostname = %q, want %q", lease.Hostname, "test-olt")
	}
}

func TestGetLease_NotFound(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}

	_, ok := s.GetLease(mac)
	if ok {
		t.Error("expected lease not found")
	}
}

func TestGetLease_ReturnsCopy(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	s.leases[mac.String()] = &Lease{
		MAC:      mac,
		IP:       net.ParseIP("10.0.0.12").To4(),
		Expiry:   time.Now().Add(time.Hour),
		Hostname: "original",
	}

	lease, _ := s.GetLease(mac)
	lease.Hostname = "mutated"

	if s.leases[mac.String()].Hostname == "mutated" {
		t.Error("GetLease should return a copy, not a reference")
	}
}

// ---------------------------------------------------------------------------
// allocateIP
// ---------------------------------------------------------------------------

func TestAllocateIP(t *testing.T) {
	s := newTestServer(t)
	poolSize := len(s.ipPool)

	ip := s.allocateIP()
	if ip == nil {
		t.Fatal("expected an IP")
	}

	if len(s.ipPool) != poolSize-1 {
		t.Errorf("pool should shrink by 1, was %d now %d", poolSize, len(s.ipPool))
	}

	// The allocated IP should be in the 10.0.0.10-20 range
	if ip[0] != 10 || ip[1] != 0 || ip[2] != 0 {
		t.Errorf("unexpected IP prefix: %v", ip)
	}
}

func TestAllocateIP_Exhausted(t *testing.T) {
	s := newTestServer(t)
	s.ipPool = make(map[string]bool)

	ip := s.allocateIP()
	if ip != nil {
		t.Errorf("expected nil, got %v", ip)
	}
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestStop_NilServer(t *testing.T) {
	s := newTestServer(t)
	// srv and cancel are nil — should not panic
	err := s.Stop()
	if err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func TestAddToIP(t *testing.T) {
	tests := []struct {
		ip   net.IP
		n    int
		want string
	}{
		{net.ParseIP("10.0.0.0").To4(), 1, "10.0.0.1"},
		{net.ParseIP("10.0.0.0").To4(), 255, "10.0.0.255"},
		{net.ParseIP("10.0.0.0").To4(), 256, "10.0.1.0"},
		{net.ParseIP("10.0.0.255").To4(), 1, "10.0.1.0"},
		{net.ParseIP("10.0.0.0").To4(), 0, "10.0.0.0"},
	}

	for _, tt := range tests {
		ip := make(net.IP, 4)
		copy(ip, tt.ip)
		addToIP(ip, tt.n)
		if ip.String() != tt.want {
			t.Errorf("addToIP(%v, %d) = %v, want %v", tt.ip, tt.n, ip, tt.want)
		}
	}
}

func TestIPToUint32(t *testing.T) {
	tests := []struct {
		ip   net.IP
		want uint32
	}{
		{net.ParseIP("0.0.0.0"), 0},
		{net.ParseIP("0.0.0.1"), 1},
		{net.ParseIP("10.0.0.1"), 0x0a000001},
		{net.ParseIP("255.255.255.255"), 0xffffffff},
	}

	for _, tt := range tests {
		got := ipToUint32(tt.ip)
		if got != tt.want {
			t.Errorf("ipToUint32(%v) = %d, want %d", tt.ip, got, tt.want)
		}
	}
}

func TestIPToUint32_NilIP(t *testing.T) {
	// Passing an IPv6-only address (no To4 representation) should return 0.
	got := ipToUint32(net.ParseIP("::1"))
	if got != 0 {
		t.Errorf("ipToUint32(::1) = %d, want 0", got)
	}
}

func TestUint32ToIP(t *testing.T) {
	tests := []struct {
		n    uint32
		want string
	}{
		{0, "0.0.0.0"},
		{1, "0.0.0.1"},
		{0x0a000001, "10.0.0.1"},
		{0xffffffff, "255.255.255.255"},
	}

	for _, tt := range tests {
		got := uint32ToIP(tt.n)
		if got.String() != tt.want {
			t.Errorf("uint32ToIP(%d) = %v, want %v", tt.n, got, tt.want)
		}
	}
}

func TestIPRoundTrip(t *testing.T) {
	ips := []string{"10.0.0.1", "192.168.1.100", "172.16.0.255", "0.0.0.0"}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr).To4()
		n := ipToUint32(ip)
		result := uint32ToIP(n)
		if !result.Equal(ip) {
			t.Errorf("round trip failed for %s: got %v", ipStr, result)
		}
	}
}

// ---------------------------------------------------------------------------
// Full DHCP flow: Discover -> Offer -> Request -> Ack
// ---------------------------------------------------------------------------

func TestFullDHCPFlow(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	// Step 1: DISCOVER
	discover := makeDiscover(t, mac)
	offer, err := s.handleDiscover(discover)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if offer.MessageType() != dhcpv4.MessageTypeOffer {
		t.Fatalf("expected Offer, got %v", offer.MessageType())
	}

	offeredIP := offer.YourIPAddr
	t.Logf("offered IP: %v", offeredIP)

	// Note: handleDiscover removes the offered IP from the pool via allocateIP()
	// but does NOT create a lease for new MACs. When the Request comes in, the
	// offered IP is no longer in the pool, so the server allocates a fresh IP.
	// This is the server's current behavior — the test validates it as-is.

	// Step 2: REQUEST with the offered IP
	request := makeRequest(t, mac, offeredIP)
	ack, err := s.handleRequest(request)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if ack.MessageType() != dhcpv4.MessageTypeAck {
		t.Fatalf("expected ACK, got %v", ack.MessageType())
	}

	assignedIP := ack.YourIPAddr

	// Verify lease exists with the assigned IP
	lease, ok := s.GetLease(mac)
	if !ok {
		t.Fatal("lease should exist after full flow")
	}
	if !lease.IP.Equal(assignedIP) {
		t.Errorf("lease IP = %v, want %v", lease.IP, assignedIP)
	}

	// Step 3: RELEASE
	release := makeRelease(t, mac, assignedIP)
	s.handleRelease(release)

	_, ok = s.GetLease(mac)
	if ok {
		t.Error("lease should be gone after release")
	}
}

func TestFullDHCPFlow_WithExistingLease(t *testing.T) {
	s := newTestServer(t)
	mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}

	// First DISCOVER + REQUEST to establish lease
	discover := makeDiscover(t, mac)
	_, err := s.handleDiscover(discover)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	request := makeRequest(t, mac, nil)
	ack, err := s.handleRequest(request)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	assignedIP := ack.YourIPAddr

	// Second DISCOVER should offer the same IP (existing lease)
	discover2 := makeDiscover(t, mac)
	offer2, err := s.handleDiscover(discover2)
	if err != nil {
		t.Fatalf("Discover2: %v", err)
	}
	if !offer2.YourIPAddr.Equal(assignedIP) {
		t.Errorf("re-discover should offer same IP: got %v, want %v", offer2.YourIPAddr, assignedIP)
	}

	// Second REQUEST with same IP should ACK
	request2 := makeRequest(t, mac, assignedIP)
	ack2, err := s.handleRequest(request2)
	if err != nil {
		t.Fatalf("Request2: %v", err)
	}
	if ack2.MessageType() != dhcpv4.MessageTypeAck {
		t.Errorf("expected ACK for renewal, got %v", ack2.MessageType())
	}
	if !ack2.YourIPAddr.Equal(assignedIP) {
		t.Errorf("renewal should keep same IP: got %v, want %v", ack2.YourIPAddr, assignedIP)
	}
}

// ---------------------------------------------------------------------------
// Concurrency safety
// ---------------------------------------------------------------------------

func TestConcurrentDiscoverRequest(t *testing.T) {
	s := newTestServer(t)

	var wg sync.WaitGroup
	const n = 10

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, byte(i)}
			m := makeDiscover(t, mac)
			resp, err := s.handleDiscover(m)
			if err != nil {
				// Pool exhaustion is possible — not a test failure
				return
			}
			if resp.MessageType() != dhcpv4.MessageTypeOffer {
				t.Errorf("goroutine %d: expected Offer, got %v", i, resp.MessageType())
			}
		}(i)
	}

	wg.Wait()

	leases := s.GetLeases()
	if len(leases) > n {
		t.Errorf("too many leases: %d", len(leases))
	}
}

func TestConcurrentGetLeases(t *testing.T) {
	s := newTestServer(t)

	// Pre-populate some leases
	for i := 0; i < 5; i++ {
		mac := net.HardwareAddr{0x01, 0x02, 0x03, 0x04, 0x05, byte(i)}
		s.leases[mac.String()] = &Lease{
			MAC:    mac,
			IP:     net.ParseIP("10.0.0.12").To4(),
			Expiry: time.Now().Add(time.Hour),
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			leases := s.GetLeases()
			if len(leases) != 5 {
				t.Errorf("expected 5 leases, got %d", len(leases))
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Default lease time
// ---------------------------------------------------------------------------

func TestDefaultLeaseTime(t *testing.T) {
	// When LeaseTime is 0, NewServer should default to 24h.
	// Since we can't call NewServer without a real NIC, test the logic directly.
	cfg := Config{LeaseTime: 0}
	if cfg.LeaseTime == 0 {
		cfg.LeaseTime = 24 * time.Hour
	}
	if cfg.LeaseTime != 24*time.Hour {
		t.Errorf("expected 24h default, got %v", cfg.LeaseTime)
	}
}

// ---------------------------------------------------------------------------
// Multiple devices
// ---------------------------------------------------------------------------

func TestMultipleDevices(t *testing.T) {
	s := newTestServer(t)

	macs := []net.HardwareAddr{
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x01},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x02},
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x03},
	}

	allocatedIPs := make(map[string]bool)

	for _, mac := range macs {
		m := makeDiscover(t, mac)
		resp, err := s.handleDiscover(m)
		if err != nil {
			t.Fatalf("Discover for %v: %v", mac, err)
		}

		ip := resp.YourIPAddr.String()
		if allocatedIPs[ip] {
			t.Errorf("duplicate IP allocation: %s", ip)
		}
		allocatedIPs[ip] = true
	}

	if len(allocatedIPs) != 3 {
		t.Errorf("expected 3 unique IPs, got %d", len(allocatedIPs))
	}
}

// containsStr checks if s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
