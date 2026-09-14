package storage

import (
	"io"
	"os"
)

// FSStat is filesystem capacity at a path.
type FSStat struct {
	BlockSize   int64
	Blocks      uint64
	BlocksFree  uint64
	BlocksAvail uint64
	Dev         uint64
}

func (s FSStat) usable() int64 {
	if s.BlockSize <= 0 {
		return 0
	}
	return int64(s.BlocksAvail) * s.BlockSize
}

func (s FSStat) total() int64 {
	if s.BlockSize <= 0 {
		return 0
	}
	return int64(s.Blocks) * s.BlockSize
}

// Host abstracts privileged filesystem facts so tests can inject fixtures.
type Host struct {
	ReadMounts func() (string, error)
	StatFS     func(path string) (FSStat, error)
	Eval       func(path string) (string, error)
	LookupUUID func(device string) string
	SetXattr   func(path, name, value string) error
	GetXattr   func(path, name string) (string, error)
	QEMU       ImageRunner
	QEMUBin    string
	OpenFile   func(name string, flag int, perm os.FileMode) (*os.File, error)
	Remove     func(name string) error
	MkdirAll   func(path string, perm os.FileMode) error
	Stat       func(name string) (os.FileInfo, error)
	ReadDir    func(name string) ([]os.DirEntry, error)
	WriteFile  func(name string, data []byte, perm os.FileMode) error
	ReadFile   func(name string) ([]byte, error)
	Rename     func(oldpath, newpath string) error
	Allocated  func(path string) (int64, error)
	WalkSize   func(path string) (allocated, logical int64, err error)
}

func liveHost() Host {
	h := Host{}
	h.ReadMounts = readMountsLive
	h.StatFS = statFSLive
	h.Eval = evalLive
	h.LookupUUID = lookupUUIDLive
	h.SetXattr = setXattrLive
	h.GetXattr = getXattrLive
	h.QEMU = defaultRunner
	h.QEMUBin = QEMUImgPath
	h.OpenFile = os.OpenFile
	h.Remove = os.Remove
	h.MkdirAll = os.MkdirAll
	h.Stat = os.Stat
	h.ReadDir = os.ReadDir
	h.WriteFile = os.WriteFile
	h.ReadFile = os.ReadFile
	h.Rename = os.Rename
	h.Allocated = allocatedLive
	h.WalkSize = walkSizeLive
	return h
}

func (h Host) withDefaults() Host {
	live := liveHost()
	if h.ReadMounts == nil {
		h.ReadMounts = live.ReadMounts
	}
	if h.StatFS == nil {
		h.StatFS = live.StatFS
	}
	if h.Eval == nil {
		h.Eval = live.Eval
	}
	if h.LookupUUID == nil {
		h.LookupUUID = live.LookupUUID
	}
	if h.SetXattr == nil {
		h.SetXattr = live.SetXattr
	}
	if h.GetXattr == nil {
		h.GetXattr = live.GetXattr
	}
	if h.QEMU == nil {
		h.QEMU = live.QEMU
	}
	if h.QEMUBin == "" {
		h.QEMUBin = live.QEMUBin
	}
	if h.OpenFile == nil {
		h.OpenFile = live.OpenFile
	}
	if h.Remove == nil {
		h.Remove = live.Remove
	}
	if h.MkdirAll == nil {
		h.MkdirAll = live.MkdirAll
	}
	if h.Stat == nil {
		h.Stat = live.Stat
	}
	if h.ReadDir == nil {
		h.ReadDir = live.ReadDir
	}
	if h.WriteFile == nil {
		h.WriteFile = live.WriteFile
	}
	if h.ReadFile == nil {
		h.ReadFile = live.ReadFile
	}
	if h.Rename == nil {
		h.Rename = live.Rename
	}
	if h.Allocated == nil {
		h.Allocated = live.Allocated
	}
	if h.WalkSize == nil {
		h.WalkSize = live.WalkSize
	}
	return h
}

func copyFile(dst, src *os.File) error {
	_, err := io.Copy(dst, src)
	return err
}
