package storage

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

const (
	ext4SuperOffset   = 1024
	ext4SuperLen      = 1024
	ext4Magic         = 0xEF53
	ext4Incompat64Bit = 0x80
)

// Ext4FilesystemBytes reads an ext2/3/4 superblock and returns the size the
// filesystem occupies. Shrink uses it to prove the filesystem fits before the
// backing image is truncated.
func Ext4FilesystemBytes(r io.ReaderAt) (int64, error) {
	sb := make([]byte, ext4SuperLen)
	if _, err := r.ReadAt(sb, ext4SuperOffset); err != nil {
		return 0, fmt.Errorf("read ext4 superblock: %w", err)
	}
	if binary.LittleEndian.Uint16(sb[0x38:]) != ext4Magic {
		return 0, fmt.Errorf("image does not hold an ext4 filesystem")
	}
	logBlock := binary.LittleEndian.Uint32(sb[0x18:])
	if logBlock > 6 {
		return 0, fmt.Errorf("ext4 block size is invalid")
	}
	blockSize := int64(1024) << logBlock
	blocks := uint64(binary.LittleEndian.Uint32(sb[0x04:]))
	if binary.LittleEndian.Uint32(sb[0x60:])&ext4Incompat64Bit != 0 {
		blocks |= uint64(binary.LittleEndian.Uint32(sb[0x150:])) << 32
	}
	if blocks == 0 || blocks > uint64(MaxVolumeBytes/blockSize) {
		return 0, fmt.Errorf("ext4 block count is invalid")
	}
	return int64(blocks) * blockSize, nil
}

func ext4ImageBytes(img string) (int64, error) {
	f, err := os.Open(img)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return Ext4FilesystemBytes(f)
}

// ShrinkFloor is the smallest size a disk holding used bytes may shrink to:
// the data plus 10% or 1 GiB of headroom, whichever is larger.
func ShrinkFloor(used int64) int64 {
	headroom := used / 10
	if headroom < 1<<30 {
		headroom = 1 << 30
	}
	floor := used + headroom
	if floor < MinRootBytes {
		floor = MinRootBytes
	}
	return floor
}

// ShrinkTooSmallError explains why a requested shrink was refused.
func ShrinkTooSmallError(used, floor int64) error {
	return fmt.Errorf("the disk holds %s of data, so it cannot shrink below %s", humanGiB(used), humanGiB(floor))
}

func humanGiB(n int64) string {
	return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
}
