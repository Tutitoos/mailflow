package metrics

import "testing"

func TestRegistryRejectsHighCardinalityDimensions(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Set("sync_total", "counter", 1, map[string]string{"provider": "google"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Set("sync_total", "counter", 1, map[string]string{"account_id": "private"}); err == nil {
		t.Fatal("expected account identifiers to be rejected as dimensions")
	}
}
