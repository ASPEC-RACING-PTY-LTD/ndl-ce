package ctbackup

import "golang.org/x/sys/unix"

func unixSetxattr(path, name string, val []byte) error {
	return unix.Setxattr(path, name, val, 0)
}

func unixGetxattr(path, name string) ([]byte, error) {
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}
