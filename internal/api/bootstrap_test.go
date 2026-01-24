package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/codelaboratoryltd/nexus/internal/hashring"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// setupTestServerWithDevices creates a test server with device store support.
func setupTestServerWithDevices() (*Server, *mockPoolStore, *mockNodeStore, *mockAllocationStore, *store.InMemoryDeviceStore) {
	ring := hashring.NewVirtualNodesHashRing()
	poolStore := newMockPoolStore()
	nodeStore := newMockNodeStore()
	allocStore := newMockAllocationStore()
	deviceStore := store.NewInMemoryDeviceStore()

	server := NewServerWithDevices(ring, poolStore, nodeStore, allocStore, deviceStore)
	return server, poolStore, nodeStore, allocStore, deviceStore
}

func TestBootstrap_NewDevice(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"model": "OLT-BNG-1000",
		"firmware": "1.0.0"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("bootstrap new device returned status %d, want %d: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var response BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.NodeID == "" {
		t.Error("bootstrap response missing node_id")
	}

	if response.Status != "pending" {
		t.Errorf("bootstrap response status = %s, want pending", response.Status)
	}

	if response.RetryAfter != 30 {
		t.Errorf("bootstrap response retry_after = %d, want 30", response.RetryAfter)
	}
}

func TestBootstrap_ReRegistration(t *testing.T) {
	server, _, _, _, deviceStore := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// First registration
	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"firmware": "1.0.0"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("first bootstrap returned status %d: %s", w.Code, w.Body.String())
	}

	var firstResponse BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &firstResponse); err != nil {
		t.Fatalf("Failed to unmarshal first response: %v", err)
	}

	// Wait a moment to ensure LastSeen will be different
	time.Sleep(10 * time.Millisecond)

	// Re-registration with updated firmware
	body = `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"firmware": "1.1.0"
	}`
	req = httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Re-registration should return 200 OK, not 201 Created
	if w.Code != http.StatusOK {
		t.Errorf("re-registration returned status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var secondResponse BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &secondResponse); err != nil {
		t.Fatalf("Failed to unmarshal second response: %v", err)
	}

	// Node ID should be the same
	if secondResponse.NodeID != firstResponse.NodeID {
		t.Errorf("re-registration changed node_id from %s to %s", firstResponse.NodeID, secondResponse.NodeID)
	}

	// Verify firmware was updated in store
	device, err := deviceStore.GetDeviceBySerial(nil, "GPON12345678")
	if err != nil {
		t.Fatalf("Failed to get device: %v", err)
	}
	if device.Firmware != "1.1.0" {
		t.Errorf("device firmware = %s, want 1.1.0", device.Firmware)
	}
}

func TestBootstrap_MissingSerial(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"mac": "00:11:22:33:44:55"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap without serial returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_MissingMAC(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "GPON12345678"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap without MAC returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_InvalidMAC(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "GPON12345678",
		"mac": "not-a-mac"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with invalid MAC returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_InvalidSerial(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "ab",
		"mac": "00:11:22:33:44:55"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with invalid serial returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_InvalidJSON(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{invalid json}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with invalid JSON returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_MACFormatNormalization(t *testing.T) {
	testCases := []struct {
		name        string
		inputMAC    string
		expectedMAC string
		serial      string
	}{
		{"colon separated", "00:11:22:33:44:55", "00:11:22:33:44:55", "GPON00000001"},
		{"dash separated", "00-11-22-33-44-66", "00:11:22:33:44:66", "GPON00000002"},
		{"no separator", "001122334477", "00:11:22:33:44:77", "GPON00000003"},
		{"lowercase", "aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF", "GPON00000004"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create fresh server for each test to avoid MAC collision
			server, _, _, _, deviceStore := setupTestServerWithDevices()
			router := mux.NewRouter()
			server.RegisterRoutes(router)

			body := `{
				"serial": "` + tc.serial + `",
				"mac": "` + tc.inputMAC + `"
			}`
			req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusCreated {
				t.Errorf("bootstrap returned status %d: %s", w.Code, w.Body.String())
				return
			}

			// Verify MAC was normalized
			device, err := deviceStore.GetDeviceBySerial(nil, tc.serial)
			if err != nil {
				t.Fatalf("Failed to get device: %v", err)
			}
			if device.MAC != tc.expectedMAC {
				t.Errorf("device MAC = %s, want %s", device.MAC, tc.expectedMAC)
			}
		})
	}
}

func TestBootstrap_DeterministicNodeID(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55"
	}`

	// First request
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var firstResponse BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &firstResponse); err != nil {
		t.Fatalf("Failed to unmarshal first response: %v", err)
	}

	// Delete the device to simulate a fresh registration
	server.deviceStore.DeleteDevice(nil, firstResponse.NodeID)

	// Second request with same serial/MAC
	req = httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var secondResponse BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &secondResponse); err != nil {
		t.Fatalf("Failed to unmarshal second response: %v", err)
	}

	// Node ID should be the same since it's deterministic based on serial + MAC
	if secondResponse.NodeID != firstResponse.NodeID {
		t.Errorf("deterministic node_id mismatch: got %s, expected %s", secondResponse.NodeID, firstResponse.NodeID)
	}
}

func TestListDevices_Empty(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/v1/devices", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("list devices returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	devices, ok := response["devices"].([]interface{})
	if !ok {
		t.Fatal("Response does not contain devices array")
	}
	if len(devices) != 0 {
		t.Errorf("Expected empty devices, got %d", len(devices))
	}
}

func TestListDevices_WithDevices(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Register two devices
	for i, serial := range []string{"GPON11111111", "GPON22222222"} {
		body := `{
			"serial": "` + serial + `",
			"mac": "00:11:22:33:44:5` + string(rune('0'+i)) + `"
		}`
		req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("bootstrap %d returned status %d", i, w.Code)
		}
	}

	// List devices
	req := httptest.NewRequest("GET", "/api/v1/devices", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("list devices returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := response["count"].(float64)
	if !ok || count != 2 {
		t.Errorf("Expected count 2, got %v", response["count"])
	}
}

func TestListDevices_FilterByStatus(t *testing.T) {
	server, _, _, _, deviceStore := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Register a device (will be pending)
	body := `{
		"serial": "GPON11111111",
		"mac": "00:11:22:33:44:55"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var response BootstrapResponse
	json.Unmarshal(w.Body.Bytes(), &response)

	// Manually update one device to configured status
	device, _ := deviceStore.GetDevice(nil, response.NodeID)
	device.Status = store.DeviceStatusConfigured
	deviceStore.SaveDevice(nil, device)

	// Register another device (will remain pending)
	body = `{
		"serial": "GPON22222222",
		"mac": "00:11:22:33:44:56"
	}`
	req = httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Filter by pending status
	req = httptest.NewRequest("GET", "/api/v1/devices?status=pending", nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("list devices returned status %d, want %d", w.Code, http.StatusOK)
	}

	var listResponse map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &listResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	count, ok := listResponse["count"].(float64)
	if !ok || count != 1 {
		t.Errorf("Expected count 1 for pending devices, got %v", listResponse["count"])
	}
}

func TestGetDevice_Found(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Register a device
	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"model": "OLT-BNG-1000"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var bootstrapResponse BootstrapResponse
	json.Unmarshal(w.Body.Bytes(), &bootstrapResponse)

	// Get the device
	req = httptest.NewRequest("GET", "/api/v1/devices/"+bootstrapResponse.NodeID, nil)
	w = httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("get device returned status %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var deviceResponse DeviceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &deviceResponse); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if deviceResponse.NodeID != bootstrapResponse.NodeID {
		t.Errorf("device node_id = %s, want %s", deviceResponse.NodeID, bootstrapResponse.NodeID)
	}
	if deviceResponse.Serial != "GPON12345678" {
		t.Errorf("device serial = %s, want GPON12345678", deviceResponse.Serial)
	}
	if deviceResponse.Model != "OLT-BNG-1000" {
		t.Errorf("device model = %s, want OLT-BNG-1000", deviceResponse.Model)
	}
}

func TestGetDevice_NotFound(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	req := httptest.NewRequest("GET", "/api/v1/devices/nonexistent-node", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("get nonexistent device returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestNormalizeMAC(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"00:11:22:33:44:55", "00:11:22:33:44:55"},
		{"00-11-22-33-44-55", "00:11:22:33:44:55"},
		{"001122334455", "00:11:22:33:44:55"},
		{"aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF"},
		{"AA:BB:CC:DD:EE:FF", "AA:BB:CC:DD:EE:FF"},
		{"aabb.ccdd.eeff", "AA:BB:CC:DD:EE:FF"}, // Cisco format
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeMAC(tc.input)
			if result != tc.expected {
				t.Errorf("normalizeMAC(%s) = %s, want %s", tc.input, result, tc.expected)
			}
		})
	}
}

func TestGenerateNodeID(t *testing.T) {
	// Test determinism
	serial := "GPON12345678"
	mac := "00:11:22:33:44:55"

	id1 := generateNodeID(serial, mac)
	id2 := generateNodeID(serial, mac)

	if id1 != id2 {
		t.Errorf("generateNodeID not deterministic: %s != %s", id1, id2)
	}

	// Test prefix
	if !strings.HasPrefix(id1, "node-") {
		t.Errorf("generateNodeID should have 'node-' prefix, got %s", id1)
	}

	// Test uniqueness with different inputs
	id3 := generateNodeID("GPON87654321", mac)
	if id1 == id3 {
		t.Error("generateNodeID should produce different IDs for different serials")
	}

	id4 := generateNodeID(serial, "AA:BB:CC:DD:EE:FF")
	if id1 == id4 {
		t.Error("generateNodeID should produce different IDs for different MACs")
	}
}

func TestBootstrap_ConfiguredDevice(t *testing.T) {
	server, _, _, _, deviceStore := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Register a device
	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var firstResponse BootstrapResponse
	json.Unmarshal(w.Body.Bytes(), &firstResponse)

	// Manually update device to configured status with pool assignments
	device, _ := deviceStore.GetDevice(nil, firstResponse.NodeID)
	device.Status = store.DeviceStatusConfigured
	device.SiteID = "site-001"
	device.Role = store.DeviceRoleActive
	device.PartnerNodeID = "node-partner"
	device.AssignedPools = []string{"pool1"}
	deviceStore.SaveDevice(nil, device)

	// Re-bootstrap should return configured device info
	req = httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("bootstrap configured device returned status %d, want %d", w.Code, http.StatusOK)
	}

	var response BootstrapResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.Status != "configured" {
		t.Errorf("response status = %s, want configured", response.Status)
	}
	if response.SiteID != "site-001" {
		t.Errorf("response site_id = %s, want site-001", response.SiteID)
	}
	if response.Role != "active" {
		t.Errorf("response role = %s, want active", response.Role)
	}
	if response.Partner == nil {
		t.Error("response should have partner info")
	} else if response.Partner.NodeID != "node-partner" {
		t.Errorf("response partner node_id = %s, want node-partner", response.Partner.NodeID)
	}
	if response.RetryAfter != 0 {
		t.Errorf("configured device should not have retry_after, got %d", response.RetryAfter)
	}
}

func TestBootstrap_WithoutDeviceStore(t *testing.T) {
	// Test that bootstrap endpoint is not available without device store
	server, _, _, _ := setupTestServer()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Should get 404 since route is not registered without device store
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("bootstrap without device store returned status %d, want 404 or 405", w.Code)
	}
}

func TestBootstrap_LongFirmwareVersion(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Create a firmware string longer than 64 characters
	longFirmware := strings.Repeat("x", 65)
	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"firmware": "` + longFirmware + `"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with long firmware returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBootstrap_LongPublicKey(t *testing.T) {
	server, _, _, _, _ := setupTestServerWithDevices()
	router := mux.NewRouter()
	server.RegisterRoutes(router)

	// Create a public key string longer than 4096 characters
	longKey := strings.Repeat("x", 4097)
	body := `{
		"serial": "GPON12345678",
		"mac": "00:11:22:33:44:55",
		"public_key": "` + longKey + `"
	}`
	req := httptest.NewRequest("POST", "/api/v1/bootstrap", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bootstrap with long public_key returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}
