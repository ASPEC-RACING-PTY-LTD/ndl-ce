//go:build !unix

package iojail

import "os"

func fillOwner(_ *FileMeta, _ os.FileInfo) {}
