//go:build !unix

package downloads

import "os"

func exclusiveCreateFlags() int {
	return os.O_RDWR | os.O_CREATE | os.O_EXCL
}

func existingWriteFlags(append bool) int {
	flags := os.O_WRONLY
	if append {
		flags |= os.O_APPEND
	}
	return flags
}

func reopenWriteFlags() int {
	return os.O_WRONLY | os.O_TRUNC
}
