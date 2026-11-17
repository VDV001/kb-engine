package main

import (
	"context"
	"crypto/ed25519"

	"github.com/daniil/kb-engine/internal/usecase/selfupdate"
)

type updateState struct {
	Current   string `json:"current"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Dev       bool   `json:"dev"`
	Busy      bool   `json:"busy"`
	Error     string `json:"error"`
}

type updateDesk struct{ tmp string }

func newUpdateDesk(string, selfupdate.Source, ed25519.PublicKey, string, func(context.Context, string, string) error) *updateDesk {
	return &updateDesk{tmp: "."}
}

func (d *updateDesk) check(context.Context)         {}
func (d *updateDesk) State() updateState            { return updateState{} }
func (d *updateDesk) Install(context.Context) error { return nil }
func releasePublicKey() ed25519.PublicKey           { return nil }
