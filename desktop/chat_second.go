package main

import "github.com/daniil/kb-engine/internal/domain"

type hintView struct {
	Role   string `json:"role"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

func (d *chatDesk) SetSecondModel(string) error { return nil }

func (d *chatDesk) Hint(string, int) hintView { return hintView{} }

var _ = domain.RoleMain
