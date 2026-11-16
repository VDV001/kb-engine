//go:build !windows

package main

import (
	"fmt"
	"os"
)

// Помощник собирается только под Windows; на Маке диктовку ведет desktop/listen (Swift).
func main() {
	fmt.Println(`{"error":"kb-listen.exe — помощник диктовки для Windows"}`)
	os.Exit(2)
}
