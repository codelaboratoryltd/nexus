package keys

import (
	"testing"

	ds "github.com/ipfs/go-datastore"
)

func TestKeyDefinitions(t *testing.T) {
	// Test that all key variables are non-nil
	keys := []struct {
		name string
		key  interface{}
	}{
		{"NexusKey", NexusKey},
		{"PoolKey", PoolKey},
		{"AllocationKey", AllocationKey},
		{"SubscriberKey", SubscriberKey},
		{"NodeKey", NodeKey},
	}

	for _, k := range keys {
		t.Run(k.name, func(t *testing.T) {
			if k.key == nil {
				t.Errorf("%s is nil", k.name)
			}
		})
	}
}

func TestNexusKey(t *testing.T) {
	expectedStr := "/nexus"
	if NexusKey.String() != expectedStr {
		t.Errorf("NexusKey.String() = %s, want %s", NexusKey.String(), expectedStr)
	}
}

func TestPoolKey(t *testing.T) {
	expectedStr := "/pool"
	if PoolKey.String() != expectedStr {
		t.Errorf("PoolKey.String() = %s, want %s", PoolKey.String(), expectedStr)
	}
}

func TestAllocationKey(t *testing.T) {
	expectedStr := "/allocation"
	if AllocationKey.String() != expectedStr {
		t.Errorf("AllocationKey.String() = %s, want %s", AllocationKey.String(), expectedStr)
	}
}

func TestSubscriberKey(t *testing.T) {
	expectedStr := "/subscriber"
	if SubscriberKey.String() != expectedStr {
		t.Errorf("SubscriberKey.String() = %s, want %s", SubscriberKey.String(), expectedStr)
	}
}

func TestNodeKey(t *testing.T) {
	expectedStr := "/node"
	if NodeKey.String() != expectedStr {
		t.Errorf("NodeKey.String() = %s, want %s", NodeKey.String(), expectedStr)
	}
}

func TestKeyChildString(t *testing.T) {
	// Test creating child keys
	childKey := PoolKey.ChildString("pool1")
	expectedStr := "/pool/pool1"
	if childKey.String() != expectedStr {
		t.Errorf("ChildString() = %s, want %s", childKey.String(), expectedStr)
	}
}

func TestKeyIsAncestorOf(t *testing.T) {
	childKey := PoolKey.ChildString("pool1")

	if !childKey.IsDescendantOf(PoolKey) {
		t.Error("Child key should be descendant of PoolKey")
	}

	if !childKey.IsDescendantOf(PoolKey) {
		t.Error("IsDescendantOf should return true for parent key")
	}
}

func TestKeyNamespaces(t *testing.T) {
	key := AllocationKey.ChildString("pool1").ChildString("sub1")
	namespaces := key.Namespaces()

	// Should contain: allocation, pool1, sub1
	if len(namespaces) != 3 {
		t.Errorf("Expected 3 namespaces, got %d: %v", len(namespaces), namespaces)
	}
}

func TestKeyEquality(t *testing.T) {
	// Same keys should be equal
	key1 := PoolKey.ChildString("pool1")
	key2 := PoolKey.ChildString("pool1")

	if !key1.Equal(key2) {
		t.Error("Identical keys should be equal")
	}

	// Different keys should not be equal
	key3 := PoolKey.ChildString("pool2")
	if key1.Equal(key3) {
		t.Error("Different keys should not be equal")
	}
}

func TestKeyTypes(t *testing.T) {
	// Ensure keys can be used as map keys (implicit test of hashability)
	keyMap := make(map[string]bool)
	keyMap[NexusKey.String()] = true
	keyMap[PoolKey.String()] = true

	if !keyMap[NexusKey.String()] {
		t.Error("NexusKey not found in map")
	}
	if !keyMap[PoolKey.String()] {
		t.Error("PoolKey not found in map")
	}
}

func TestKeyParent(t *testing.T) {
	childKey := PoolKey.ChildString("pool1")
	parentKey := childKey.Parent()

	if !parentKey.Equal(PoolKey) {
		t.Errorf("Parent key = %s, want %s", parentKey.String(), PoolKey.String())
	}
}

func TestKeyBaseNamespace(t *testing.T) {
	key := AllocationKey.ChildString("pool1").ChildString("sub1")
	baseNs := key.BaseNamespace()

	if baseNs != "sub1" {
		t.Errorf("BaseNamespace() = %s, want sub1", baseNs)
	}
}

func TestAllocationKeyStructure(t *testing.T) {
	// Test the expected structure for allocation keys
	// /allocation/pool1/sub1/offset
	key := AllocationKey.ChildString("pool1").ChildString("sub1").ChildString("AQ")

	namespaces := key.Namespaces()
	if len(namespaces) != 4 {
		t.Errorf("Expected 4 namespaces for allocation key, got %d", len(namespaces))
	}

	if namespaces[0] != "allocation" {
		t.Errorf("First namespace = %s, want allocation", namespaces[0])
	}
	if namespaces[1] != "pool1" {
		t.Errorf("Second namespace = %s, want pool1", namespaces[1])
	}
	if namespaces[2] != "sub1" {
		t.Errorf("Third namespace = %s, want sub1", namespaces[2])
	}
	if namespaces[3] != "AQ" {
		t.Errorf("Fourth namespace = %s, want AQ", namespaces[3])
	}
}

func TestSubscriberKeyStructure(t *testing.T) {
	// Test the expected structure for subscriber keys
	// /subscriber/sub1/pool1/offset
	key := SubscriberKey.ChildString("sub1").ChildString("pool1").ChildString("AQ")

	namespaces := key.Namespaces()
	if len(namespaces) != 4 {
		t.Errorf("Expected 4 namespaces for subscriber key, got %d", len(namespaces))
	}

	if namespaces[0] != "subscriber" {
		t.Errorf("First namespace = %s, want subscriber", namespaces[0])
	}
	if namespaces[1] != "sub1" {
		t.Errorf("Second namespace = %s, want sub1", namespaces[1])
	}
}

func TestKeyIsDescendantOf(t *testing.T) {
	tests := []struct {
		name         string
		key          ds.Key
		parent       ds.Key
		isDescendant bool
	}{
		{
			name:         "direct child is descendant",
			key:          PoolKey.ChildString("pool1"),
			parent:       PoolKey,
			isDescendant: true,
		},
		{
			name:         "nested child is descendant",
			key:          AllocationKey.ChildString("pool1").ChildString("sub1"),
			parent:       AllocationKey,
			isDescendant: true,
		},
		{
			name:         "unrelated key is not descendant",
			key:          PoolKey.ChildString("pool1"),
			parent:       AllocationKey,
			isDescendant: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.IsDescendantOf(tt.parent); got != tt.isDescendant {
				t.Errorf("IsDescendantOf() = %v, want %v", got, tt.isDescendant)
			}
		})
	}
}
