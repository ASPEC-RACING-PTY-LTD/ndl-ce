package cdc

// gear is the FastCDC gear-hash table: one 64-bit value per byte value. It is
// generated deterministically from a fixed seed with splitmix64 so boundaries
// are identical across processes and releases. Treat the seed as part of the
// repository format: changing it changes every chunk boundary.
var gear = buildGear(0x3f6a8885a308d313)

func buildGear(seed uint64) [256]uint64 {
	var t [256]uint64
	s := seed
	for i := range t {
		s += 0x9e3779b97f4a7c15
		z := s
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		t[i] = z
	}
	return t
}
