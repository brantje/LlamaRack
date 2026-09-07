//go:build unix

package downloads

import (
	"os"
	"syscall"
)

func exclusiveCreateFlags() int {
	return os.O_RDWR | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW
}

func existingWriteFlags(append bool) int {
	flags := os.O_WRONLY | syscall.O_NOFOLLOW
	if append {
		flags |= os.O_APPEND
	}
	return flags
}

func reopenWriteFlags() int {
	return os.O_WRONLY | os.O_TRUNC | syscall.O_NOFOLLOW
}
