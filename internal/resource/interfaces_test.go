package resource

import (
	"testing"
)

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()
	if registry == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if registry.types == nil {
		t.Error("Registry types map is nil")
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	registry := NewRegistry()

	// Create a mock resource type
	mockType := &mockResourceType{name: "test"}

	// Register the type
	registry.Register(mockType)

	// Get the type back
	rt, ok := registry.Get("test")
	if !ok {
		t.Error("Get should return true for registered type")
	}
	if rt == nil {
		t.Error("Get should return non-nil type")
	}
	if rt.Name() != "test" {
		t.Errorf("Get returned type with name %s, want test", rt.Name())
	}

	// Try to get non-existent type
	_, ok = registry.Get("nonexistent")
	if ok {
		t.Error("Get should return false for non-existent type")
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()

	// Register multiple types
	types := []string{"type1", "type2", "type3"}
	for _, name := range types {
		registry.Register(&mockResourceType{name: name})
	}

	// List all types
	list := registry.List()
	if len(list) != len(types) {
		t.Errorf("List returned %d types, want %d", len(list), len(types))
	}

	// Verify all types are present
	found := make(map[string]bool)
	for _, name := range list {
		found[name] = true
	}
	for _, name := range types {
		if !found[name] {
			t.Errorf("Type %s not found in list", name)
		}
	}
}

func TestRegistry_Empty(t *testing.T) {
	registry := NewRegistry()

	// List should return empty slice
	list := registry.List()
	if len(list) != 0 {
		t.Errorf("Empty registry List returned %d types, want 0", len(list))
	}
}

func TestRegistry_Overwrite(t *testing.T) {
	registry := NewRegistry()

	// Register a type
	mockType1 := &mockResourceType{name: "test", version: 1}
	registry.Register(mockType1)

	// Register another type with the same name
	mockType2 := &mockResourceType{name: "test", version: 2}
	registry.Register(mockType2)

	// Get should return the second type
	rt, ok := registry.Get("test")
	if !ok {
		t.Error("Get should return true")
	}
	if rt.(*mockResourceType).version != 2 {
		t.Error("Registry should overwrite with later registration")
	}
}

// mockResourceType is a test implementation of the Type interface
type mockResourceType struct {
	name    string
	version int
}

func (m *mockResourceType) Name() string {
	return m.name
}

func (m *mockResourceType) NewPool(config map[string]interface{}) (Pool, error) {
	return nil, nil
}

func (m *mockResourceType) NewAllocator(pool Pool, ranges []Range) (Allocator, error) {
	return nil, nil
}

func (m *mockResourceType) ParseResource(s string) (Resource, error) {
	return nil, nil
}

func (m *mockResourceType) ParseRange(s string) (Range, error) {
	return nil, nil
}

func TestResourceErrors(t *testing.T) {
	// Test that all error variables are non-nil
	errors := []error{
		ErrPoolExhausted,
		ErrResourceNotInPool,
		ErrResourceAlreadyAllocated,
		ErrResourceNotAllocated,
		ErrInvalidConfig,
		ErrInvalidRange,
		ErrCannotSplit,
		ErrTypeMismatch,
	}

	for _, err := range errors {
		if err == nil {
			t.Error("Error variable should not be nil")
		}
		if err.Error() == "" {
			t.Error("Error message should not be empty")
		}
	}
}

func TestErrPoolExhausted(t *testing.T) {
	err := ErrPoolExhausted
	expectedMsg := "pool exhausted: no available resources"
	if err.Error() != expectedMsg {
		t.Errorf("ErrPoolExhausted.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrResourceNotInPool(t *testing.T) {
	err := ErrResourceNotInPool
	expectedMsg := "resource not in pool"
	if err.Error() != expectedMsg {
		t.Errorf("ErrResourceNotInPool.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrResourceAlreadyAllocated(t *testing.T) {
	err := ErrResourceAlreadyAllocated
	expectedMsg := "resource already allocated"
	if err.Error() != expectedMsg {
		t.Errorf("ErrResourceAlreadyAllocated.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrResourceNotAllocated(t *testing.T) {
	err := ErrResourceNotAllocated
	expectedMsg := "resource not allocated"
	if err.Error() != expectedMsg {
		t.Errorf("ErrResourceNotAllocated.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrInvalidConfig(t *testing.T) {
	err := ErrInvalidConfig
	expectedMsg := "invalid pool configuration"
	if err.Error() != expectedMsg {
		t.Errorf("ErrInvalidConfig.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrInvalidRange(t *testing.T) {
	err := ErrInvalidRange
	expectedMsg := "invalid range specification"
	if err.Error() != expectedMsg {
		t.Errorf("ErrInvalidRange.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrCannotSplit(t *testing.T) {
	err := ErrCannotSplit
	expectedMsg := "cannot split range into requested number of parts"
	if err.Error() != expectedMsg {
		t.Errorf("ErrCannotSplit.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}

func TestErrTypeMismatch(t *testing.T) {
	err := ErrTypeMismatch
	expectedMsg := "resource type mismatch"
	if err.Error() != expectedMsg {
		t.Errorf("ErrTypeMismatch.Error() = %q, want %q", err.Error(), expectedMsg)
	}
}
