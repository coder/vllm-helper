package detect

import (
	"bytes"
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"
)

// KERN_PROCARGS2 hands back the argv a process was started with, NUL
// separated, the way /proc/<pid>/cmdline does on Linux. ps can only give us
// the same thing joined by spaces, which is ambiguous for any argument that
// contains one.
const (
	ctlKern       = 1
	kernProcArgs2 = 49
)

// procArgs returns the exact argv of pid. It only works for processes the
// caller owns (or when running as root); everything else gets EINVAL, which
// callers treat as "fall back to ps".
func procArgs(pid int) ([]string, error) {
	mib := [3]int32{ctlKern, kernProcArgs2, int32(pid)}

	var n uintptr
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3,
		0, uintptr(unsafe.Pointer(&n)), 0, 0); errno != 0 {
		return nil, errno
	}
	if n == 0 {
		return nil, errors.New("empty argv buffer")
	}

	buf := make([]byte, n)
	if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), 3,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0, 0); errno != 0 {
		return nil, errno
	}
	return parseProcArgs(buf[:n])
}

// parseProcArgs decodes a KERN_PROCARGS2 buffer: a 32-bit argc, the
// executable path, NUL padding, then argc NUL-terminated arguments (the
// environment follows, which we don't want).
func parseProcArgs(buf []byte) ([]string, error) {
	if len(buf) < 4 {
		return nil, errors.New("argv buffer too short")
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc <= 0 {
		return nil, errors.New("no arguments")
	}
	rest := buf[4:]

	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, errors.New("no executable path")
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	args := make([]string, 0, argc)
	for range argc {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			// Truncated buffer: keep what parsed rather than losing it all.
			if len(rest) > 0 {
				args = append(args, string(rest))
			}
			break
		}
		args = append(args, string(rest[:i]))
		rest = rest[i+1:]
	}
	if len(args) == 0 {
		return nil, errors.New("no arguments")
	}
	return args, nil
}
