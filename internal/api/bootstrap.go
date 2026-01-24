package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
	"github.com/codelaboratoryltd/nexus/internal/validation"
)

// BootstrapRequest represents a device bootstrap registration request.
type BootstrapRequest struct {
	// Serial is the hardware serial number (required).
	Serial string `json:"serial"`

	// MAC is the primary MAC address (required).
	MAC string `json:"mac"`

	// Model is the device model identifier (optional).
	Model string `json:"model,omitempty"`

	// Firmware is the current firmware version (optional).
	Firmware string `json:"firmware,omitempty"`

	// PublicKey is the device's public key for mTLS (optional, for future use).
	PublicKey string `json:"public_key,omitempty"`
}

// PartnerInfo contains information about an HA partner device.
type PartnerInfo struct {
	// NodeID is the partner's node identifier.
	NodeID string `json:"node_id"`

	// Address is the partner's management address.
	Address string `json:"address,omitempty"`

	// Status indicates if the partner is online.
	Status string `json:"status,omitempty"`
}

// PoolAssignment represents a pool assigned to a device.
type PoolAssignment struct {
	// PoolID is the pool identifier.
	PoolID string `json:"pool_id"`

	// CIDR is the pool's IP range.
	CIDR string `json:"cidr"`

	// Subnets are the specific subnets assigned to this node.
	Subnets []string `json:"subnets,omitempty"`
}

// ClusterInfo contains information about the Nexus cluster.
type ClusterInfo struct {
	// Peers is the list of Nexus peer addresses.
	Peers []string `json:"peers,omitempty"`

	// SyncEndpoint is the endpoint for state synchronization.
	SyncEndpoint string `json:"sync_endpoint,omitempty"`
}

// BootstrapResponse is returned to devices after successful bootstrap.
type BootstrapResponse struct {
	// NodeID is the unique identifier assigned to this device.
	NodeID string `json:"node_id"`

	// Status is the device's current status ("configured" or "pending").
	Status string `json:"status"`

	// SiteID is the assigned site identifier (if configured).
	SiteID string `json:"site_id,omitempty"`

	// Role is the device role in an HA pair ("active" or "standby").
	Role string `json:"role,omitempty"`

	// Partner contains HA partner information (if configured).
	Partner *PartnerInfo `json:"partner,omitempty"`

	// Pools is the list of assigned IP pools (if configured).
	Pools []PoolAssignment `json:"pools,omitempty"`

	// Cluster contains Nexus cluster information.
	Cluster *ClusterInfo `json:"cluster,omitempty"`

	// RetryAfter is seconds to wait before retrying (for pending devices).
	RetryAfter int `json:"retry_after,omitempty"`

	// Message provides additional context about the response.
	Message string `json:"message,omitempty"`
}

// bootstrap handles device bootstrap registration.
// POST /api/v1/bootstrap
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse request body
	var req BootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: malformed JSON")
		return
	}

	// Validate required fields
	if err := s.validateBootstrapRequest(&req); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Normalize MAC address to uppercase with colons
	normalizedMAC := normalizeMAC(req.MAC)

	// Check if device already exists (by serial or MAC)
	existingDevice, err := s.deviceStore.GetDeviceBySerial(ctx, req.Serial)
	if err != nil && err != store.ErrDeviceNotFound {
		respondError(w, http.StatusInternalServerError, "failed to check device registration")
		return
	}

	if existingDevice == nil {
		// Also check by MAC
		existingDevice, err = s.deviceStore.GetDeviceByMAC(ctx, normalizedMAC)
		if err != nil && err != store.ErrDeviceNotFound {
			respondError(w, http.StatusInternalServerError, "failed to check device registration")
			return
		}
	}

	now := time.Now()
	var device *store.Device

	if existingDevice != nil {
		// Re-registration: update last seen and any changed fields
		device = existingDevice
		device.LastSeen = now

		// Update mutable fields if provided
		if req.Firmware != "" {
			device.Firmware = req.Firmware
		}
		if req.PublicKey != "" {
			device.PublicKey = req.PublicKey
		}
		if req.Model != "" && device.Model == "" {
			device.Model = req.Model
		}
	} else {
		// New registration
		nodeID := generateNodeID(req.Serial, normalizedMAC)

		device = &store.Device{
			NodeID:    nodeID,
			Serial:    req.Serial,
			MAC:       normalizedMAC,
			Model:     req.Model,
			Firmware:  req.Firmware,
			PublicKey: req.PublicKey,
			Status:    store.DeviceStatusPending,
			FirstSeen: now,
			LastSeen:  now,
		}
	}

	// Save the device
	if err := s.deviceStore.SaveDevice(ctx, device); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to save device registration")
		return
	}

	// Build response based on device status
	response := s.buildBootstrapResponse(ctx, device)

	// Return appropriate status code
	statusCode := http.StatusOK
	if existingDevice == nil {
		statusCode = http.StatusCreated
	}

	respondJSON(w, statusCode, response)
}

// validateBootstrapRequest validates the bootstrap request fields.
func (s *Server) validateBootstrapRequest(req *BootstrapRequest) error {
	// Validate serial number
	if err := validation.ValidateSerialNumber(req.Serial); err != nil {
		return err
	}

	// Validate MAC address
	if _, err := validation.ValidateMACAddress(req.MAC); err != nil {
		return err
	}

	// Validate model if provided
	if req.Model != "" {
		if len(req.Model) > 64 {
			return validation.NewValidationError("model", req.Model, "model exceeds maximum length of 64", validation.ErrValueTooLong)
		}
	}

	// Validate firmware version if provided
	if req.Firmware != "" {
		if len(req.Firmware) > 64 {
			return validation.NewValidationError("firmware", req.Firmware, "firmware version exceeds maximum length of 64", validation.ErrValueTooLong)
		}
	}

	// Validate public key if provided (basic length check)
	if req.PublicKey != "" {
		if len(req.PublicKey) > 4096 {
			return validation.NewValidationError("public_key", "", "public key exceeds maximum length of 4096", validation.ErrValueTooLong)
		}
	}

	return nil
}

// buildBootstrapResponse constructs the response based on device state.
func (s *Server) buildBootstrapResponse(ctx context.Context, device *store.Device) *BootstrapResponse {
	response := &BootstrapResponse{
		NodeID: device.NodeID,
		Status: string(device.Status),
	}

	if device.Status == store.DeviceStatusPending {
		// Device is pending configuration
		response.RetryAfter = 30 // Check again in 30 seconds
		response.Message = "Device registered, awaiting configuration"
		return response
	}

	// Device is configured - include full configuration
	response.SiteID = device.SiteID
	response.Role = string(device.Role)

	// Add partner info if available
	if device.PartnerNodeID != "" {
		response.Partner = &PartnerInfo{
			NodeID: device.PartnerNodeID,
			Status: "unknown", // Would need to check partner status
		}
	}

	// Add assigned pools with their subnet assignments
	if len(device.AssignedPools) > 0 {
		response.Pools = s.getPoolAssignments(ctx, device)
	}

	// Add cluster info
	response.Cluster = s.getClusterInfo(ctx)

	response.Message = "Device configured successfully"
	return response
}

// getPoolAssignments retrieves pool assignments for a device.
func (s *Server) getPoolAssignments(ctx context.Context, device *store.Device) []PoolAssignment {
	assignments := make([]PoolAssignment, 0, len(device.AssignedPools))

	for _, poolID := range device.AssignedPools {
		pool, err := s.poolStore.GetPool(ctx, poolID)
		if err != nil {
			continue // Skip pools that can't be found
		}

		assignment := PoolAssignment{
			PoolID: poolID,
			CIDR:   pool.CIDR.String(),
		}

		// Get subnets assigned to this node from the hashring
		if s.ring != nil {
			subnets, err := s.ring.ListPoolSubnetsAtNode(hashring.NodeID(device.NodeID))
			if err == nil {
				if poolSubnets, ok := subnets[hashring.PoolID(poolID)]; ok {
					for _, subnet := range poolSubnets {
						assignment.Subnets = append(assignment.Subnets, subnet.String())
					}
				}
			}
		}

		assignments = append(assignments, assignment)
	}

	return assignments
}

// getClusterInfo retrieves current cluster information.
func (s *Server) getClusterInfo(ctx context.Context) *ClusterInfo {
	info := &ClusterInfo{
		Peers: []string{}, // Would be populated from cluster state
	}

	// Get node list from nodeStore
	nodes, err := s.nodeStore.ListNodes(ctx)
	if err == nil {
		for _, node := range nodes {
			if addr, ok := node.Metadata["address"]; ok {
				info.Peers = append(info.Peers, addr)
			}
		}
	}

	return info
}

// generateNodeID creates a deterministic node ID from serial and MAC.
func generateNodeID(serial, mac string) string {
	// Create a deterministic ID by hashing serial + MAC
	input := serial + ":" + mac
	hash := sha256.Sum256([]byte(input))
	// Use first 8 bytes (16 hex chars) for a shorter ID
	return "node-" + hex.EncodeToString(hash[:8])
}

// normalizeMAC converts a MAC address to uppercase with colons.
func normalizeMAC(mac string) string {
	// Remove any existing separators
	mac = strings.ReplaceAll(mac, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")
	mac = strings.ToUpper(mac)

	// Insert colons
	if len(mac) == 12 {
		return mac[0:2] + ":" + mac[2:4] + ":" + mac[4:6] + ":" +
			mac[6:8] + ":" + mac[8:10] + ":" + mac[10:12]
	}

	return mac
}

// DeviceResponse represents a device in API responses.
type DeviceResponse struct {
	NodeID        string            `json:"node_id"`
	Serial        string            `json:"serial"`
	MAC           string            `json:"mac"`
	Model         string            `json:"model,omitempty"`
	Firmware      string            `json:"firmware,omitempty"`
	Status        string            `json:"status"`
	SiteID        string            `json:"site_id,omitempty"`
	Role          string            `json:"role,omitempty"`
	PartnerNodeID string            `json:"partner_node_id,omitempty"`
	AssignedPools []string          `json:"assigned_pools,omitempty"`
	FirstSeen     time.Time         `json:"first_seen"`
	LastSeen      time.Time         `json:"last_seen"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// listDevices returns all registered devices.
// GET /api/v1/devices
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Optional filter by site_id
	siteID := r.URL.Query().Get("site_id")
	// Optional filter by status
	statusFilter := r.URL.Query().Get("status")

	var devices []*store.Device
	var err error

	if siteID != "" {
		devices, err = s.deviceStore.ListDevicesBySite(ctx, siteID)
	} else {
		devices, err = s.deviceStore.ListDevices(ctx)
	}

	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list devices")
		return
	}

	// Apply status filter if provided
	var filteredDevices []*store.Device
	if statusFilter != "" {
		for _, d := range devices {
			if string(d.Status) == statusFilter {
				filteredDevices = append(filteredDevices, d)
			}
		}
	} else {
		filteredDevices = devices
	}

	response := make([]DeviceResponse, 0, len(filteredDevices))
	for _, d := range filteredDevices {
		response = append(response, deviceToResponse(d))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"devices": response,
		"count":   len(response),
	})
}

// getDevice returns a specific device by node ID.
// GET /api/v1/devices/{node_id}
func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get node_id from URL path using gorilla/mux
	vars := mux.Vars(r)
	nodeID := vars["node_id"]

	if nodeID == "" {
		respondError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	device, err := s.deviceStore.GetDevice(ctx, nodeID)
	if err == store.ErrDeviceNotFound {
		respondError(w, http.StatusNotFound, "device not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get device")
		return
	}

	respondJSON(w, http.StatusOK, deviceToResponse(device))
}

// deviceToResponse converts a store.Device to API response format.
func deviceToResponse(d *store.Device) DeviceResponse {
	return DeviceResponse{
		NodeID:        d.NodeID,
		Serial:        d.Serial,
		MAC:           d.MAC,
		Model:         d.Model,
		Firmware:      d.Firmware,
		Status:        string(d.Status),
		SiteID:        d.SiteID,
		Role:          string(d.Role),
		PartnerNodeID: d.PartnerNodeID,
		AssignedPools: d.AssignedPools,
		FirstSeen:     d.FirstSeen,
		LastSeen:      d.LastSeen,
		Metadata:      d.Metadata,
	}
}

// AssignDeviceRequest represents a request to assign a device to a site.
type AssignDeviceRequest struct {
	// SiteID is the site identifier to assign the device to.
	SiteID string `json:"site_id"`
}

// AssignDeviceResponse is returned after assigning a device to a site.
type AssignDeviceResponse struct {
	// NodeID is the device's unique identifier.
	NodeID string `json:"node_id"`

	// SiteID is the assigned site identifier.
	SiteID string `json:"site_id"`

	// Role is the device role in an HA pair ("active" or "standby").
	Role string `json:"role"`

	// PartnerNodeID is the node ID of the HA partner (if paired).
	PartnerNodeID string `json:"partner_node_id,omitempty"`

	// Status is the device's current status.
	Status string `json:"status"`

	// Message provides additional context about the assignment.
	Message string `json:"message"`
}

// ImportResponse is returned after bulk importing devices from CSV.
type ImportResponse struct {
	// Imported is the number of devices successfully imported.
	Imported int `json:"imported"`

	// Paired is the number of devices that got auto-paired.
	Paired int `json:"paired"`

	// Errors contains any errors encountered during import.
	Errors []string `json:"errors,omitempty"`
}

// assignDevice assigns a device to a site with auto-pairing.
// PUT /api/v1/devices/{node_id}
func (s *Server) assignDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get node_id from URL path using gorilla/mux
	vars := mux.Vars(r)
	nodeID := vars["node_id"]

	if nodeID == "" {
		respondError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	// Parse request body
	var req AssignDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: malformed JSON")
		return
	}

	// Validate site_id
	if err := validation.ValidateSiteID(req.SiteID); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Get the device
	device, err := s.deviceStore.GetDevice(ctx, nodeID)
	if err == store.ErrDeviceNotFound {
		respondError(w, http.StatusNotFound, "device not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get device")
		return
	}

	// Assign device to site with auto-pairing
	response, err := s.assignDeviceToSite(ctx, device, req.SiteID)
	if err != nil {
		// Check if this is a site capacity error
		if strings.Contains(err.Error(), "already has 2 devices") {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, "failed to assign device: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, response)
}

// assignDeviceToSite handles the assignment of a device to a site with auto-pairing logic.
func (s *Server) assignDeviceToSite(ctx context.Context, device *store.Device, siteID string) (*AssignDeviceResponse, error) {
	// Get existing devices at this site
	existing, err := s.deviceStore.ListDevicesBySite(ctx, siteID)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices at site: %w", err)
	}

	// Filter out the current device if it's already at this site
	var otherDevices []*store.Device
	for _, d := range existing {
		if d.NodeID != device.NodeID {
			otherDevices = append(otherDevices, d)
		}
	}

	var response AssignDeviceResponse
	response.NodeID = device.NodeID
	response.SiteID = siteID

	switch len(otherDevices) {
	case 0:
		// First device at site - becomes active
		device.SiteID = siteID
		device.Role = store.DeviceRoleActive
		device.Status = store.DeviceStatusConfigured
		device.PartnerNodeID = ""

		response.Role = string(store.DeviceRoleActive)
		response.Status = string(store.DeviceStatusConfigured)
		response.Message = "Device assigned as active (first device at site)"

	case 1:
		// Second device - becomes standby, pair with first
		partner := otherDevices[0]

		device.SiteID = siteID
		device.Role = store.DeviceRoleStandby
		device.Status = store.DeviceStatusConfigured
		device.PartnerNodeID = partner.NodeID

		// Update partner to know about this device
		partner.PartnerNodeID = device.NodeID
		if err := s.deviceStore.SaveDevice(ctx, partner); err != nil {
			return nil, fmt.Errorf("failed to update partner device: %w", err)
		}

		response.Role = string(store.DeviceRoleStandby)
		response.Status = string(store.DeviceStatusConfigured)
		response.PartnerNodeID = partner.NodeID
		response.Message = fmt.Sprintf("Device assigned as standby, paired with %s", partner.NodeID)

	default:
		return nil, fmt.Errorf("site %s already has 2 devices", siteID)
	}

	// Save the device
	if err := s.deviceStore.SaveDevice(ctx, device); err != nil {
		return nil, fmt.Errorf("failed to save device: %w", err)
	}

	return &response, nil
}

// importDevices imports devices from a CSV file.
// POST /api/v1/devices/import
// CSV format: serial,site_id
func (s *Server) importDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check content type
	contentType := r.Header.Get("Content-Type")

	var csvReader *csv.Reader

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Handle multipart form data
		if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB max
			respondError(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			respondError(w, http.StatusBadRequest, "failed to get uploaded file: "+err.Error())
			return
		}
		defer file.Close()

		csvReader = csv.NewReader(file)
	} else {
		// Assume text/csv or application/octet-stream - read directly from body
		csvReader = csv.NewReader(r.Body)
	}

	// Read all CSV records
	records, err := csvReader.ReadAll()
	if err != nil {
		respondError(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	if len(records) == 0 {
		respondError(w, http.StatusBadRequest, "CSV file is empty")
		return
	}

	// Check if first row is a header
	startRow := 0
	firstRow := records[0]
	if len(firstRow) >= 2 {
		// If first row looks like a header, skip it
		if strings.EqualFold(firstRow[0], "serial") && strings.EqualFold(firstRow[1], "site_id") {
			startRow = 1
		}
	}

	response := ImportResponse{
		Errors: []string{},
	}

	for i := startRow; i < len(records); i++ {
		row := records[i]
		lineNum := i + 1

		if len(row) < 2 {
			response.Errors = append(response.Errors, fmt.Sprintf("line %d: expected 2 columns, got %d", lineNum, len(row)))
			continue
		}

		serial := strings.TrimSpace(row[0])
		siteID := strings.TrimSpace(row[1])

		// Skip empty rows
		if serial == "" && siteID == "" {
			continue
		}

		// Validate serial number
		if err := validation.ValidateSerialNumber(serial); err != nil {
			response.Errors = append(response.Errors, fmt.Sprintf("line %d: %s", lineNum, err.Error()))
			continue
		}

		// Validate site_id
		if err := validation.ValidateSiteID(siteID); err != nil {
			response.Errors = append(response.Errors, fmt.Sprintf("line %d: %s", lineNum, err.Error()))
			continue
		}

		// Check if device already exists by serial
		existingDevice, err := s.deviceStore.GetDeviceBySerial(ctx, serial)
		if err != nil && err != store.ErrDeviceNotFound {
			response.Errors = append(response.Errors, fmt.Sprintf("line %d: failed to check device: %s", lineNum, err.Error()))
			continue
		}

		var device *store.Device
		now := time.Now()

		if existingDevice != nil {
			// Device already registered - assign to site
			device = existingDevice
		} else {
			// Pre-configure device before it registers
			// Generate a placeholder node ID based on serial
			// (will be regenerated with proper MAC when device actually bootstraps)
			nodeID := generateNodeID(serial, "00:00:00:00:00:00")

			device = &store.Device{
				NodeID:    nodeID,
				Serial:    serial,
				Status:    store.DeviceStatusPending, // Will be set to configured by assignDeviceToSite
				FirstSeen: now,
				LastSeen:  now,
			}

			// Save the pre-configured device first
			if err := s.deviceStore.SaveDevice(ctx, device); err != nil {
				response.Errors = append(response.Errors, fmt.Sprintf("line %d: failed to create device: %s", lineNum, err.Error()))
				continue
			}
		}

		// Assign device to site
		assignResp, err := s.assignDeviceToSite(ctx, device, siteID)
		if err != nil {
			response.Errors = append(response.Errors, fmt.Sprintf("line %d (serial %s): %s", lineNum, serial, err.Error()))
			continue
		}

		response.Imported++

		// Check if device was paired
		if assignResp.PartnerNodeID != "" {
			response.Paired++
		}
	}

	respondJSON(w, http.StatusOK, response)
}

// deleteDevice removes a device from the system and unpairs its partner if necessary.
// DELETE /api/v1/devices/{node_id}
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get node_id from URL path using gorilla/mux
	vars := mux.Vars(r)
	nodeID := vars["node_id"]

	if nodeID == "" {
		respondError(w, http.StatusBadRequest, "node_id is required")
		return
	}

	// Get the device first to check for partner
	device, err := s.deviceStore.GetDevice(ctx, nodeID)
	if err == store.ErrDeviceNotFound {
		respondError(w, http.StatusNotFound, "device not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get device")
		return
	}

	// If device has a partner, unpair them
	if device.PartnerNodeID != "" {
		partner, err := s.deviceStore.GetDevice(ctx, device.PartnerNodeID)
		if err == nil {
			// Clear partner's reference to this device and promote to active
			partner.PartnerNodeID = ""
			partner.Role = store.DeviceRoleActive
			if err := s.deviceStore.SaveDevice(ctx, partner); err != nil {
				// Log but don't fail - best effort to update partner
			}
		}
	}

	// Delete the device
	if err := s.deviceStore.DeleteDevice(ctx, nodeID); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to delete device")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "device deleted successfully",
		"node_id": nodeID,
	})
}

// parseCSVLine parses a single line of CSV data.
func parseCSVLine(line string) ([]string, error) {
	reader := csv.NewReader(strings.NewReader(line))
	return reader.Read()
}

// streamingCSVReader reads CSV records one at a time.
type streamingCSVReader struct {
	scanner *bufio.Scanner
}

func newStreamingCSVReader(r io.Reader) *streamingCSVReader {
	return &streamingCSVReader{
		scanner: bufio.NewScanner(r),
	}
}

func (s *streamingCSVReader) Read() ([]string, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return parseCSVLine(s.scanner.Text())
}
