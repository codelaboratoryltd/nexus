// Package ztp provides Zero Touch Provisioning for OLT-BNG devices.
// It includes a DHCP server that assigns management IPs and provides
// Nexus URL via DHCP options for automatic device registration.
package ztp

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
)

// Config holds ZTP DHCP server configuration.
type Config struct {
	// Interface to listen on
	Interface string

	// Management network pool
	Network    net.IPNet
	RangeStart net.IP
	RangeEnd   net.IP
	Gateway    net.IP
	DNS        []net.IP
	LeaseTime  time.Duration

	// Nexus URL to provide to devices (e.g., "http://nexus.local:9000")
	NexusURL string
}

// Lease represents a DHCP lease for a device.
type Lease struct {
	MAC        net.HardwareAddr
	IP         net.IP
	Expiry     time.Time
	Hostname   string
	VendorInfo string
}

// Server is a DHCP server for OLT-BNG ZTP.
type Server struct {
	config    Config
	serverIP  net.IP
	serverMAC net.HardwareAddr

	mu     sync.RWMutex
	leases map[string]*Lease // keyed by MAC string
	ipPool map[string]bool   // available IPs

	srv    *server4.Server
	ctx    context.Context
	cancel context.CancelFunc

	// Callbacks for device events
	OnDeviceDiscovered func(mac net.HardwareAddr, hostname, vendorInfo string)
	OnDeviceLeased     func(mac net.HardwareAddr, ip net.IP)
}

// NewServer creates a new ZTP DHCP server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Interface == "" {
		return nil, fmt.Errorf("interface is required")
	}
	if cfg.Network.IP == nil {
		return nil, fmt.Errorf("network is required")
	}
	if cfg.NexusURL == "" {
		return nil, fmt.Errorf("nexus URL is required")
	}

	// Default lease time
	if cfg.LeaseTime == 0 {
		cfg.LeaseTime = 24 * time.Hour
	}

	// Get interface info
	iface, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %s: %w", cfg.Interface, err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface addresses: %w", err)
	}

	var serverIP net.IP
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			serverIP = ipnet.IP.To4()
			break
		}
	}
	if serverIP == nil {
		return nil, fmt.Errorf("no IPv4 address found on interface %s", cfg.Interface)
	}

	s := &Server{
		config:    cfg,
		serverIP:  serverIP,
		serverMAC: iface.HardwareAddr,
		leases:    make(map[string]*Lease),
		ipPool:    make(map[string]bool),
	}

	// Initialize IP pool
	if err := s.initPool(); err != nil {
		return nil, err
	}

	return s, nil
}

// initPool initializes the available IP pool.
func (s *Server) initPool() error {
	start := s.config.RangeStart.To4()
	end := s.config.RangeEnd.To4()

	if start == nil || end == nil {
		// Default: use network range excluding gateway
		ones, bits := s.config.Network.Mask.Size()
		size := 1 << (bits - ones)

		base := s.config.Network.IP.To4()
		// Skip network address and gateway (first two), and broadcast (last)
		for i := 2; i < size-1; i++ {
			ip := make(net.IP, 4)
			copy(ip, base)
			addToIP(ip, i)

			// Skip server IP
			if ip.Equal(s.serverIP) {
				continue
			}
			// Skip gateway
			if s.config.Gateway != nil && ip.Equal(s.config.Gateway.To4()) {
				continue
			}

			s.ipPool[ip.String()] = true
		}
	} else {
		// Use specified range
		startVal := ipToUint32(start)
		endVal := ipToUint32(end)

		for i := startVal; i <= endVal; i++ {
			ip := uint32ToIP(i)

			// Skip server IP
			if ip.Equal(s.serverIP) {
				continue
			}
			// Skip gateway
			if s.config.Gateway != nil && ip.Equal(s.config.Gateway.To4()) {
				continue
			}

			s.ipPool[ip.String()] = true
		}
	}

	if len(s.ipPool) == 0 {
		return fmt.Errorf("no available IPs in pool")
	}

	return nil
}

// Start starts the DHCP server.
func (s *Server) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	laddr := net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 67,
	}

	srv, err := server4.NewServer(s.config.Interface, &laddr, s.handler)
	if err != nil {
		return fmt.Errorf("failed to create DHCP server: %w", err)
	}
	s.srv = srv

	// Start lease cleanup goroutine
	go s.cleanupLeases()

	return srv.Serve()
}

// Stop stops the DHCP server.
func (s *Server) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// handler processes incoming DHCP packets.
func (s *Server) handler(conn net.PacketConn, peer net.Addr, m *dhcpv4.DHCPv4) {
	if m == nil {
		return
	}

	var resp *dhcpv4.DHCPv4
	var err error

	switch m.MessageType() {
	case dhcpv4.MessageTypeDiscover:
		resp, err = s.handleDiscover(m)
	case dhcpv4.MessageTypeRequest:
		resp, err = s.handleRequest(m)
	case dhcpv4.MessageTypeRelease:
		s.handleRelease(m)
		return
	default:
		return
	}

	if err != nil {
		fmt.Printf("ZTP DHCP error: %v\n", err)
		return
	}
	if resp == nil {
		return
	}

	// Send response
	var addr net.Addr
	if resp.GatewayIPAddr != nil && !resp.GatewayIPAddr.IsUnspecified() {
		addr = &net.UDPAddr{IP: resp.GatewayIPAddr, Port: 67}
	} else if resp.ClientIPAddr != nil && !resp.ClientIPAddr.IsUnspecified() {
		addr = &net.UDPAddr{IP: resp.ClientIPAddr, Port: 68}
	} else {
		addr = &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
	}

	if _, err := conn.WriteTo(resp.ToBytes(), addr); err != nil {
		fmt.Printf("ZTP DHCP send error: %v\n", err)
	}
}

// handleDiscover handles DHCP DISCOVER messages.
func (s *Server) handleDiscover(m *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	mac := m.ClientHWAddr
	macStr := mac.String()

	// Extract device info
	hostname := m.HostName()
	vendorInfo := ""
	// Option 60: Vendor Class Identifier
	if vi := m.Options.Get(dhcpv4.GenericOptionCode(60)); vi != nil {
		vendorInfo = string(vi)
	}

	// Callback for device discovery
	if s.OnDeviceDiscovered != nil {
		s.OnDeviceDiscovered(mac, hostname, vendorInfo)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for existing lease
	var offerIP net.IP
	if lease, exists := s.leases[macStr]; exists {
		offerIP = lease.IP
	} else {
		// Allocate new IP
		offerIP = s.allocateIP()
		if offerIP == nil {
			return nil, fmt.Errorf("no available IPs")
		}
	}

	return s.buildResponse(m, dhcpv4.MessageTypeOffer, offerIP)
}

// handleRequest handles DHCP REQUEST messages.
func (s *Server) handleRequest(m *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	mac := m.ClientHWAddr
	macStr := mac.String()

	// Get requested IP
	requestedIP := m.RequestedIPAddress()
	if requestedIP == nil {
		requestedIP = m.ClientIPAddr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var assignIP net.IP

	// Check existing lease
	if lease, exists := s.leases[macStr]; exists {
		if requestedIP != nil && !requestedIP.Equal(lease.IP) {
			// Client requesting different IP - NAK
			return s.buildNAK(m)
		}
		assignIP = lease.IP
		lease.Expiry = time.Now().Add(s.config.LeaseTime)
	} else {
		// New lease
		if requestedIP != nil && s.ipPool[requestedIP.String()] {
			assignIP = requestedIP
			delete(s.ipPool, requestedIP.String())
		} else {
			// Allocate new
			assignIP = s.allocateIP()
			if assignIP == nil {
				return s.buildNAK(m)
			}
		}

		hostname := m.HostName()
		vendorInfo := ""
		// Option 60: Vendor Class Identifier
		if vi := m.Options.Get(dhcpv4.GenericOptionCode(60)); vi != nil {
			vendorInfo = string(vi)
		}

		s.leases[macStr] = &Lease{
			MAC:        mac,
			IP:         assignIP,
			Expiry:     time.Now().Add(s.config.LeaseTime),
			Hostname:   hostname,
			VendorInfo: vendorInfo,
		}
	}

	// Callback for device leased
	if s.OnDeviceLeased != nil {
		s.OnDeviceLeased(mac, assignIP)
	}

	return s.buildResponse(m, dhcpv4.MessageTypeAck, assignIP)
}

// handleRelease handles DHCP RELEASE messages.
func (s *Server) handleRelease(m *dhcpv4.DHCPv4) {
	mac := m.ClientHWAddr
	macStr := mac.String()

	s.mu.Lock()
	defer s.mu.Unlock()

	if lease, exists := s.leases[macStr]; exists {
		s.ipPool[lease.IP.String()] = true
		delete(s.leases, macStr)
	}
}

// allocateIP allocates an available IP from the pool.
// Must be called with s.mu held.
func (s *Server) allocateIP() net.IP {
	for ipStr := range s.ipPool {
		delete(s.ipPool, ipStr)
		return net.ParseIP(ipStr).To4()
	}
	return nil
}

// buildResponse builds a DHCP response with Nexus-specific options.
func (s *Server) buildResponse(req *dhcpv4.DHCPv4, msgType dhcpv4.MessageType, clientIP net.IP) (*dhcpv4.DHCPv4, error) {
	resp, err := dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(msgType),
		dhcpv4.WithServerIP(s.serverIP),
		dhcpv4.WithYourIP(clientIP),
	)
	if err != nil {
		return nil, err
	}

	// Standard options
	resp.UpdateOption(dhcpv4.OptSubnetMask(s.config.Network.Mask))
	resp.UpdateOption(dhcpv4.OptIPAddressLeaseTime(s.config.LeaseTime))
	resp.UpdateOption(dhcpv4.OptServerIdentifier(s.serverIP))

	if s.config.Gateway != nil {
		resp.UpdateOption(dhcpv4.OptRouter(s.config.Gateway))
	}

	if len(s.config.DNS) > 0 {
		resp.UpdateOption(dhcpv4.OptDNS(s.config.DNS...))
	}

	// Nexus-specific options:
	// Option 43 (Vendor-Specific): Contains Nexus URL
	// Format: TLV with type=1 for URL
	vendorOpts := buildNexusVendorOptions(s.config.NexusURL)
	resp.UpdateOption(dhcpv4.OptGeneric(dhcpv4.OptionVendorSpecificInformation, vendorOpts))

	// Also provide via Option 224 (private use) for simpler parsing
	resp.UpdateOption(dhcpv4.OptGeneric(dhcpv4.GenericOptionCode(224), []byte(s.config.NexusURL)))

	return resp, nil
}

// buildNAK builds a DHCP NAK response.
func (s *Server) buildNAK(req *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, error) {
	return dhcpv4.NewReplyFromRequest(req,
		dhcpv4.WithMessageType(dhcpv4.MessageTypeNak),
		dhcpv4.WithServerIP(s.serverIP),
	)
}

// buildNexusVendorOptions builds vendor-specific option data.
// Format: Type(1) Length(1) Value(n)
// Type 1 = Nexus URL
func buildNexusVendorOptions(nexusURL string) []byte {
	urlBytes := []byte(nexusURL)
	data := make([]byte, 2+len(urlBytes))
	data[0] = 1                   // Type: Nexus URL
	data[1] = byte(len(urlBytes)) // Length
	copy(data[2:], urlBytes)
	return data
}

// cleanupLeases periodically removes expired leases.
func (s *Server) cleanupLeases() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for macStr, lease := range s.leases {
				if now.After(lease.Expiry) {
					s.ipPool[lease.IP.String()] = true
					delete(s.leases, macStr)
				}
			}
			s.mu.Unlock()
		}
	}
}

// GetLeases returns all current leases.
func (s *Server) GetLeases() []*Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()

	leases := make([]*Lease, 0, len(s.leases))
	for _, l := range s.leases {
		lCopy := *l
		leases = append(leases, &lCopy)
	}
	return leases
}

// GetLease returns the lease for a specific MAC.
func (s *Server) GetLease(mac net.HardwareAddr) (*Lease, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lease, exists := s.leases[mac.String()]
	if !exists {
		return nil, false
	}
	lCopy := *lease
	return &lCopy, true
}

// Helper functions

func addToIP(ip net.IP, n int) {
	for i := len(ip) - 1; i >= 0 && n > 0; i-- {
		sum := int(ip[i]) + n
		ip[i] = byte(sum % 256)
		n = sum / 256
	}
}

func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip)
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}
