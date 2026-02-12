package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceWhitelist_AddAndCheck(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Add("SN001"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Pending device should not pass Check
	if w.Check("SN001") {
		t.Error("pending device should not pass Check")
	}

	// Approve and check
	if err := w.Approve("SN001"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if !w.Check("SN001") {
		t.Error("approved device should pass Check")
	}

	// Unknown device
	if w.Check("SN999") {
		t.Error("unknown device should not pass Check")
	}
}

func TestDeviceWhitelist_DuplicateAdd(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Add("SN001"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := w.Add("SN001"); err == nil {
		t.Error("duplicate Add should return error")
	}
}

func TestDeviceWhitelist_Revoke(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Add("SN001"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := w.Approve("SN001"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := w.Revoke("SN001"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if w.Check("SN001") {
		t.Error("revoked device should not pass Check")
	}

	entry := w.Get("SN001")
	if entry == nil {
		t.Fatal("Get returned nil for revoked device")
	}
	if entry.Status != DeviceStatusRevoked {
		t.Errorf("expected status revoked, got %s", entry.Status)
	}
	if entry.RevokedAt == nil {
		t.Error("RevokedAt should be set")
	}
}

func TestDeviceWhitelist_Remove(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Add("SN001"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := w.Remove("SN001"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if w.Get("SN001") != nil {
		t.Error("removed device should not be found")
	}

	// Remove non-existent
	if err := w.Remove("SN999"); err == nil {
		t.Error("Remove of non-existent should return error")
	}
}

func TestDeviceWhitelist_ListPending(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	w.Add("SN001")
	w.Add("SN002")
	w.Add("SN003")
	w.Approve("SN002")

	pending := w.ListPending()
	if len(pending) != 2 {
		t.Errorf("expected 2 pending, got %d", len(pending))
	}
}

func TestDeviceWhitelist_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "whitelist.json")

	// Create and populate
	w1, err := NewDeviceWhitelist(path)
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}
	w1.Add("SN001")
	w1.Approve("SN001")
	w1.Add("SN002")

	// Reload from file
	w2, err := NewDeviceWhitelist(path)
	if err != nil {
		t.Fatalf("NewDeviceWhitelist reload: %v", err)
	}

	if !w2.Check("SN001") {
		t.Error("reloaded whitelist should have approved SN001")
	}

	entry := w2.Get("SN002")
	if entry == nil {
		t.Fatal("reloaded whitelist should have SN002")
	}
	if entry.Status != DeviceStatusPending {
		t.Errorf("expected pending status, got %s", entry.Status)
	}
}

func TestDeviceWhitelist_LoadNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	w, err := NewDeviceWhitelist(path)
	if err != nil {
		t.Fatalf("should not error on non-existent file: %v", err)
	}

	if len(w.ListAll()) != 0 {
		t.Error("new whitelist should be empty")
	}
}

func TestDeviceWhitelist_LoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")

	os.WriteFile(path, []byte("not json"), 0600)

	_, err := NewDeviceWhitelist(path)
	if err == nil {
		t.Error("should error on corrupt file")
	}
}

func TestDeviceWhitelist_ApproveRevoked(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	w.Add("SN001")
	w.Approve("SN001")
	w.Revoke("SN001")

	if err := w.Approve("SN001"); err == nil {
		t.Error("approving revoked device should return error")
	}
}

func TestDeviceWhitelist_ApproveNonExistent(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Approve("SN999"); err == nil {
		t.Error("approving non-existent device should return error")
	}
}

func TestDeviceWhitelist_RevokeNonExistent(t *testing.T) {
	w, err := NewDeviceWhitelist("")
	if err != nil {
		t.Fatalf("NewDeviceWhitelist: %v", err)
	}

	if err := w.Revoke("SN999"); err == nil {
		t.Error("revoking non-existent device should return error")
	}
}
