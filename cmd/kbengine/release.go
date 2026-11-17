package main

import (
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/keychain"
)

type releaseKeyStore interface {
	Get(account string) (string, error)
	Set(account, secret string) error
}

const releaseKeyAccount = "ed25519"

var releaseKeys = func() releaseKeyStore { return keychain.Store{Service: "DVK release signing"} }

func runRelease(args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "usage: kbengine release key|sign")
	return 0
}
