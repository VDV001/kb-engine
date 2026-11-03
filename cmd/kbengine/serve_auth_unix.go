//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// tokenFileMode refuses a token file that others can read or that belongs to
// another user: такой токен уже нельзя считать секретом.
func tokenFileMode(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %v", errTokenPerms, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return errTokenOwner
	}
	return nil
}
