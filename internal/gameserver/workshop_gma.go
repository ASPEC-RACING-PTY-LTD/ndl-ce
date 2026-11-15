package gameserver

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Garry's Mod addon packs (.gma) and the compressed form Steam stores older
// Workshop items in (an LZMA stream whose header records the unpacked size).

var gmaMagic = []byte("GMAD")

// lzmaBin is the xz binary that unpacks legacy Workshop downloads.
var lzmaBin = "/usr/bin/xz"

// GMAInfo is what a pack holds.
type GMAInfo struct {
	Name  string
	Files []string
}

// HasLua reports whether the pack carries any Lua, which a running server
// cannot swap in safely. Gamemodes keep theirs outside lua/, so every .lua
// counts.
func (g GMAInfo) HasLua() bool {
	for _, f := range g.Files {
		if strings.HasSuffix(strings.ToLower(f), ".lua") {
			return true
		}
	}
	return false
}

// ReadGMA reads a pack's header and file table and checks the file is
// exactly as long as the table says, with or without the trailing CRC.
func ReadGMA(path string) (GMAInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return GMAInfo{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return GMAInfo{}, err
	}
	r := &countReader{r: bufio.NewReader(f)}
	var info GMAInfo
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || !bytes.Equal(magic, gmaMagic) {
		return info, errors.New("not a GMA file")
	}
	var ver byte
	if err := binary.Read(r, binary.LittleEndian, &ver); err != nil {
		return info, errors.New("GMA header is cut short")
	}
	if _, err := io.CopyN(io.Discard, r, 16); err != nil { // SteamID and timestamp
		return info, errors.New("GMA header is cut short")
	}
	if ver > 1 {
		for i := 0; ; i++ {
			s, err := readCString(r)
			if err != nil || i > 1024 {
				return info, errors.New("GMA header is malformed")
			}
			if s == "" {
				break
			}
		}
	}
	if info.Name, err = readCString(r); err != nil {
		return info, errors.New("GMA header is malformed")
	}
	for i := 0; i < 2; i++ { // description, author
		if _, err := readCString(r); err != nil {
			return info, errors.New("GMA header is malformed")
		}
	}
	if _, err := io.CopyN(io.Discard, r, 4); err != nil { // addon version
		return info, errors.New("GMA header is cut short")
	}
	var total int64
	for {
		var num uint32
		if err := binary.Read(r, binary.LittleEndian, &num); err != nil {
			return info, errors.New("GMA file table is cut short")
		}
		if num == 0 {
			break
		}
		name, err := readCString(r)
		if err != nil {
			return info, errors.New("GMA file table is malformed")
		}
		var size int64
		if err := binary.Read(r, binary.LittleEndian, &size); err != nil || size < 0 {
			return info, errors.New("GMA file table is malformed")
		}
		if _, err := io.CopyN(io.Discard, r, 4); err != nil { // CRC
			return info, errors.New("GMA file table is cut short")
		}
		total += size
		info.Files = append(info.Files, name)
		if len(info.Files) > 100000 {
			return info, errors.New("GMA lists too many files")
		}
	}
	end := r.n + total
	if st.Size() != end && st.Size() != end+4 {
		return info, fmt.Errorf("GMA is %d bytes, its file table says %d", st.Size(), end)
	}
	return info, nil
}

type countReader struct {
	r *bufio.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func readCString(r io.Reader) (string, error) {
	var b []byte
	one := make([]byte, 1)
	for len(b) < 4096 {
		if _, err := io.ReadFull(r, one); err != nil {
			return "", err
		}
		if one[0] == 0 {
			return string(b), nil
		}
		b = append(b, one[0])
	}
	return "", errors.New("string too long")
}

// unpackWorkshopFile turns a SteamCMD download into a plain GMA at dst: a
// GMA is copied, a legacy LZMA download is unpacked. Garry's Mod writes its
// LZMA streams without an end marker, so xz reports an error; the result is
// accepted when it is exactly the size the stream's header records.
func unpackWorkshopFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	head := make([]byte, 13)
	n, _ := io.ReadFull(in, head)
	if n >= 4 && bytes.Equal(head[:4], gmaMagic) {
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return copyToNew(in, dst)
	}
	if n < 13 || head[0] > 224 {
		return errors.New("the download is neither a GMA nor an LZMA stream")
	}
	want := binary.LittleEndian.Uint64(head[5:13])
	if want == 0 || want > 1<<40 {
		return errors.New("the LZMA header has no usable size")
	}
	if _, err := os.Stat(lzmaBin); err != nil {
		return errors.New("xz is needed to unpack older Workshop items; install xz-utils")
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(lzmaBin, "--format=lzma", "--decompress", "--stdout")
	cmd.Stdin, cmd.Stdout = in, out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	st, statErr := out.Stat()
	_ = out.Close()
	if statErr != nil || uint64(st.Size()) != want {
		_ = os.Remove(dst)
		if runErr != nil {
			return fmt.Errorf("unpacking failed: %s", strings.TrimSpace(stderr.String()))
		}
		return errors.New("unpacking gave a file of the wrong size")
	}
	return nil
}

func copyToNew(r io.Reader, dst string) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
