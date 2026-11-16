//go:build darwin

package main

import "testing"

// Команда подключения: системный mount volume с адресом в кавычках AppleScript.
func TestMountCommand(t *testing.T) {
	cmd := mountCommand("smb://nas.local/docs")
	if len(cmd.Args) != 3 || cmd.Args[0] != "/usr/bin/osascript" || cmd.Args[2] != `mount volume "smb://nas.local/docs"` {
		t.Fatalf("args = %q", cmd.Args)
	}
}
