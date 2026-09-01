package inventory

import "sort"

const arphrdLoopback = 772

func collectNICs(opt Options) []NIC {
	fs := opt.fs()
	names := fs.list("sys/class/net")
	sort.Strings(names)

	var out []NIC
	for _, name := range names {
		base := "sys/class/net/" + name
		n := NIC{Name: name}
		if idx, ok := fs.readInt(base + "/ifindex"); ok {
			n.IfIndex = idx
		}
		n.MAC = fs.readOK(base + "/address")
		if mtu, ok := fs.readInt(base + "/mtu"); ok {
			n.MTU = mtu
		}
		n.State = fs.readOK(base + "/operstate")
		if speed, ok := fs.readInt(base + "/speed"); ok && speed >= 0 {
			n.SpeedMbps = &speed
		}
		uevent := parseUevent(fs.readOK(base + "/device/uevent"))
		n.Driver = uevent["DRIVER"]
		n.PCI = uevent["PCI_SLOT_NAME"]
		n.Kind = nicKind(fs, name, base)
		out = append(out, n)
	}
	if !fs.fixture() {
		addrs := liveInterfaceAddresses()
		for i := range out {
			if a := addrs[out[i].Name]; len(a) > 0 {
				out[i].Addresses = a
			}
		}
	}
	return out
}

func nicKind(fs FS, name, base string) string {
	if name == "lo" {
		return "loopback"
	}
	if typ, ok := fs.readInt(base + "/type"); ok && typ == arphrdLoopback {
		return "loopback"
	}
	if !fs.exists(base + "/device") {
		return "virtual"
	}
	return "physical"
}
