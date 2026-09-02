package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type qemuImageMeta struct {
	VirtualSize int64  `json:"virtual-size"`
	ActualSize  int64  `json:"actual-size"`
	Format      string `json:"format"`
}

func qemuImageInfo(h Host, dest string) (qemuImageMeta, error) {
	argv, err := QEMUInfoArgv(h.QEMUBin, dest)
	if err != nil {
		return qemuImageMeta{}, err
	}
	stdout, stderr, err := runQEMUOutput(context.Background(), h.QEMU, argv)
	if err != nil {
		if stderr != "" {
			return qemuImageMeta{}, fmt.Errorf("qemu-img info: %s", strings.TrimSpace(stderr))
		}
		return qemuImageMeta{}, err
	}
	var meta qemuImageMeta
	if err := json.Unmarshal([]byte(stdout), &meta); err != nil {
		return qemuImageMeta{}, err
	}
	return meta, nil
}

func runQEMUOutput(ctx context.Context, run ImageRunner, argv []string) (string, string, error) {
	if run == nil {
		run = defaultRunner
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	return run(ctx, argv)
}

// ImageRunner executes a validated qemu-img argv. Tests replace this.
type ImageRunner func(ctx context.Context, argv []string) (stdout, stderr string, err error)

// QEMUCreateArgv builds a typed offline create command. No user option strings.
func QEMUCreateArgv(bin, format, dest string, sizeBytes int64) ([]string, error) {
	if bin == "" {
		bin = QEMUImgPath
	}
	if format != FormatQCOW2 && format != FormatRaw {
		return nil, ErrUnsupportedFmt
	}
	if dest == "" || strings.ContainsAny(dest, "\x00\n") {
		return nil, ErrForbiddenPath
	}
	if sizeBytes < MinBlockBytes || sizeBytes > MaxVolumeBytes {
		return nil, ErrInvalidSize
	}
	return []string{bin, "create", "-f", format, dest, strconv.FormatInt(sizeBytes, 10)}, nil
}

// QEMUInfoArgv builds a typed info command.
func QEMUInfoArgv(bin, dest string) ([]string, error) {
	if bin == "" {
		bin = QEMUImgPath
	}
	if dest == "" || strings.ContainsAny(dest, "\x00\n") {
		return nil, ErrForbiddenPath
	}
	return []string{bin, "info", "--output=json", dest}, nil
}

// QEMUCreateBackingArgv builds an offline external qcow2 overlay. Never used live.
func QEMUCreateBackingArgv(bin, dest, backing string) ([]string, error) {
	if bin == "" {
		bin = QEMUImgPath
	}
	if dest == "" || backing == "" || strings.ContainsAny(dest, "\x00\n") || strings.ContainsAny(backing, "\x00\n") {
		return nil, ErrForbiddenPath
	}
	if strings.Contains(dest, "..") || strings.Contains(backing, "..") {
		return nil, ErrForbiddenPath
	}
	return []string{bin, "create", "-f", FormatQCOW2, "-b", backing, "-F", FormatQCOW2, dest}, nil
}

// QEMUBackingChainArgv inspects overlay depth. It does not mutate disks.
func QEMUBackingChainArgv(bin, dest string) ([]string, error) {
	if bin == "" {
		bin = QEMUImgPath
	}
	if dest == "" || strings.ContainsAny(dest, "\x00\n") {
		return nil, ErrForbiddenPath
	}
	return []string{bin, "info", "--backing-chain", "--output=json", dest}, nil
}

func defaultRunner(ctx context.Context, argv []string) (string, string, error) {
	if len(argv) == 0 {
		return "", "", errors.New("empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func runQEMU(ctx context.Context, run ImageRunner, argv []string) error {
	if run == nil {
		run = defaultRunner
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	_, stderr, err := run(ctx, argv)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("qemu-img: %s", msg)
	}
	return nil
}

func fileNonEmpty(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("expected file, got directory")
	}
	if st.Size() <= 0 {
		return fmt.Errorf("image file is empty")
	}
	return nil
}
