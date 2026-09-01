package vmspec

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	fatBytesPerSector  = 512
	fatSectorsPerClust = 1
	fatReserved        = 1
	fatCount           = 2
	fatRootEntries     = 512
	fatTotalSectors    = 4096
	fatSectorsPerFAT   = 16
	fatRootSectors     = fatRootEntries * 32 / fatBytesPerSector
)

// BuildCIDATA writes a FAT16 volume labelled cidata with NoCloud files.
func BuildCIDATA(files map[string][]byte) ([]byte, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("cidata has no files")
	}
	img := make([]byte, fatTotalSectors*fatBytesPerSector)
	writeBoot(img)
	firstData := fatReserved + fatCount*fatSectorsPerFAT + fatRootSectors
	cluster := 2
	rootOff := fatReserved*fatBytesPerSector + fatCount*fatSectorsPerFAT*fatBytesPerSector
	rootIdx := 0
	for name, body := range files {
		if strings.ContainsAny(name, "\x00/") || name == "" || strings.Contains(name, "..") {
			return nil, fmt.Errorf("cidata file name is invalid")
		}
		if cluster+clustersFor(len(body)) >= 0xFFF8 {
			return nil, fmt.Errorf("cidata is too large")
		}
		start := cluster
		off := firstData * fatBytesPerSector
		remaining := body
		if len(remaining) == 0 {
			putFAT(img, cluster, 0xFFFF)
			cluster++
		}
		for len(remaining) > 0 {
			clOff := off + (cluster-2)*fatBytesPerSector*fatSectorsPerClust
			n := fatBytesPerSector * fatSectorsPerClust
			if len(remaining) < n {
				n = len(remaining)
			}
			copy(img[clOff:clOff+n], remaining[:n])
			remaining = remaining[n:]
			if len(remaining) == 0 {
				putFAT(img, cluster, 0xFFFF)
			} else {
				putFAT(img, cluster, uint16(cluster+1))
			}
			cluster++
		}
		ents := lfnEntries(name, start, len(body))
		if rootIdx+len(ents) > fatRootEntries {
			return nil, fmt.Errorf("cidata root directory is full")
		}
		for _, ent := range ents {
			copy(img[rootOff+rootIdx*32:], ent[:])
			rootIdx++
		}
	}
	return img, nil
}

func clustersFor(n int) int {
	if n <= 0 {
		return 1
	}
	sz := fatBytesPerSector * fatSectorsPerClust
	return (n + sz - 1) / sz
}

func writeBoot(img []byte) {
	img[0] = 0xEB
	img[1] = 0x3C
	img[2] = 0x90
	copy(img[3:], []byte("MSDOS5.0"))
	binary.LittleEndian.PutUint16(img[11:], fatBytesPerSector)
	img[13] = fatSectorsPerClust
	binary.LittleEndian.PutUint16(img[14:], fatReserved)
	img[16] = fatCount
	binary.LittleEndian.PutUint16(img[17:], fatRootEntries)
	binary.LittleEndian.PutUint16(img[19:], fatTotalSectors)
	img[21] = 0xF8
	binary.LittleEndian.PutUint16(img[22:], fatSectorsPerFAT)
	binary.LittleEndian.PutUint16(img[24:], 1)
	binary.LittleEndian.PutUint16(img[26:], 1)
	copy(img[43:], []byte("cidata     "))
	copy(img[54:], []byte("FAT16   "))
	img[510] = 0x55
	img[511] = 0xAA
	putFAT(img, 0, 0xFFF8)
	putFAT(img, 1, 0xFFFF)
}

func putFAT(img []byte, cluster int, val uint16) {
	for i := 0; i < fatCount; i++ {
		base := (fatReserved + i*fatSectorsPerFAT) * fatBytesPerSector
		binary.LittleEndian.PutUint16(img[base+cluster*2:], val)
	}
}

func lfnEntries(name string, start, size int) [][32]byte {
	short := shortName(name)
	chk := lfnChecksum(short)
	runes := utf16.Encode([]rune(name))
	chunks := (len(runes) + 12) / 13
	if chunks < 1 {
		chunks = 1
	}
	ents := make([][32]byte, 0, chunks+1)
	for i := chunks; i >= 1; i-- {
		var e [32]byte
		ord := byte(i)
		if i == chunks {
			ord |= 0x40
		}
		e[0] = ord
		e[11] = 0x0F
		e[13] = chk
		off := (i - 1) * 13
		putUTF16(e[1:11], runes, off, 5)
		putUTF16(e[14:26], runes, off+5, 6)
		putUTF16(e[28:32], runes, off+11, 2)
		ents = append(ents, e)
	}
	var dir [32]byte
	copy(dir[0:11], short[:])
	dir[11] = 0x20
	binary.LittleEndian.PutUint16(dir[26:], uint16(start))
	binary.LittleEndian.PutUint32(dir[28:], uint32(size))
	ents = append(ents, dir)
	return ents
}

func putUTF16(dst []byte, runes []uint16, off, n int) {
	for i := 0; i < n; i++ {
		idx := off + i
		var v uint16 = 0xFFFF
		if idx == len(runes) {
			v = 0
		} else if idx < len(runes) {
			v = runes[idx]
		}
		binary.LittleEndian.PutUint16(dst[i*2:], v)
	}
}

func shortName(name string) [11]byte {
	var out [11]byte
	for i := range out {
		out[i] = ' '
	}
	base := strings.ToUpper(name)
	base = strings.ReplaceAll(base, ".", "")
	base = strings.ReplaceAll(base, "-", "")
	base = strings.ReplaceAll(base, "_", "")
	if len(base) > 6 {
		base = base[:6]
	}
	copy(out[0:], []byte(base+"~1"))
	return out
}

func lfnChecksum(name [11]byte) byte {
	var sum byte
	for i := 0; i < 11; i++ {
		sum = ((sum & 1) << 7) + (sum >> 1) + name[i]
	}
	return sum
}
