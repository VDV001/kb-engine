package main

import "io"

// runMemory — `kbengine memory init|check|add` (ADR-0008).
func runMemory(args []string, stdout, stderr io.Writer) int { return 1 }
