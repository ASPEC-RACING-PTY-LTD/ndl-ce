package vmspec

import "testing"

func TestValidateUSBAndClassify(t *testing.T) {
	if err := ValidateUSB(USB{Address: "1-2", Vendor: "046d", Product: "c52b"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUSB(USB{Address: "1-2,id=x", Vendor: "046d", Product: "c52b"}); err == nil {
		t.Fatal("banned address")
	}
	if err := Validate(Normalize(Spec{
		Name: "a", Firmware: FirmwareUEFI, SecureBoot: true,
		NICs: []NIC{{NetworkID: "33333333-3333-4333-8333-333333333333"}},
	})); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Normalize(Spec{
		Name: "a", Firmware: FirmwareBIOS, SecureBoot: true,
		NICs: []NIC{{NetworkID: "33333333-3333-4333-8333-333333333333"}},
	})); err == nil {
		t.Fatal("secure boot requires uefi")
	}
	prev := Normalize(Spec{Name: "a", NICs: []NIC{{NetworkID: "33333333-3333-4333-8333-333333333333"}}})
	next := prev
	next.USBs = []USB{{Address: "1-2", Vendor: "046d", Product: "c52b"}}
	cls := ClassifyEdit(prev, next)
	if RequiresStop(cls) || RequiresRestart(cls) {
		t.Fatalf("usb should be live: %v", cls)
	}
	next = prev
	next.SecureBoot = true
	next.Firmware = FirmwareUEFI
	cls = ClassifyEdit(prev, next)
	if !RequiresStop(cls) {
		t.Fatal("secure boot requires stop")
	}
	next = prev
	next.CPUs = 8
	cls = ClassifyEdit(prev, next)
	if !RequiresRestart(cls) || RequiresStop(cls) {
		t.Fatalf("cpu hotplug is not live: %v", cls)
	}
}
