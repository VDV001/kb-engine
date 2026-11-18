package main

import "github.com/daniil/kb-engine/internal/adapter/monium"

// moniumFromEnv builds the Monium sender from KB_MONIUM_* (#440).
func moniumFromEnv(getenv func(string) string) (*monium.Writer, error) { return nil, nil }
