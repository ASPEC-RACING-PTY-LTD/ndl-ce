package control

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/inventory"
)

func TestInventoryFingerprintIgnoresClockAndLiveGauges(t *testing.T) {
	a := inventory.Inventory{
		SchemaVersion: inventory.SchemaVersion,
		ObservedAt:    time.Unix(1, 0).UTC(),
		CPU:           inventory.CPU{Status: inventory.StatusAvailable, Model: "x"},
		Memory:        inventory.Memory{Status: inventory.StatusAvailable, TotalBytes: 100},
	}
	avail := uint64(40)
	a.Memory.AvailableBytes = &avail
	b := a
	b.ObservedAt = time.Unix(2, 0).UTC()
	used := uint64(60)
	b.Memory.UsedBytes = &used
	b.Temperatures = []inventory.Sensor{{ID: "t1", Status: inventory.StatusAvailable}}
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	if inventoryFingerprint(ra) != inventoryFingerprint(rb) {
		t.Fatal("fingerprint must ignore observed_at, live memory, and temps")
	}
	b.CPU.Model = "y"
	rb, _ = json.Marshal(b)
	if inventoryFingerprint(ra) == inventoryFingerprint(rb) {
		t.Fatal("fingerprint must notice CPU model change")
	}
}

func TestSparseInventory(t *testing.T) {
	if !sparseInventory(inventory.Inventory{}) {
		t.Fatal("empty is sparse")
	}
	if sparseInventory(inventory.Inventory{
		CPU: inventory.CPU{Status: inventory.StatusAvailable, Model: "x"},
	}) {
		t.Fatal("cpu available is not sparse")
	}
}
