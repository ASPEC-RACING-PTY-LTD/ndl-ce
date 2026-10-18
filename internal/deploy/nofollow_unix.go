//go:build unix

package deploy

import "syscall"

const noFollow = syscall.O_NOFOLLOW
