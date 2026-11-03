//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// tokenFileMode refuses a token file that anyone outside the owner and its
// group can read or write, a group can write, or that belongs to a user other
// than this one or root: такой токен уже нельзя считать секретом.
//
// Чтение группе и владелец root разрешены ради Secret в Kubernetes: том
// монтируется от root с правами 0440 и группой пода (fsGroup), а сервер
// работает непривилегированным пользователем.
func tokenFileMode(info os.FileInfo) error {
	if info.Mode().Perm()&0o027 != 0 {
		return fmt.Errorf("%w: %v", errTokenPerms, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() && st.Uid != 0 {
		return errTokenOwner
	}
	return nil
}
