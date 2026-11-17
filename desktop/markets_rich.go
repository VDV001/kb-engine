package main

import (
	"context"
	"net/http"
)

type richBlockView struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Photo string `json:"photo"`
}

type richVariantView struct {
	Name   string          `json:"name"`
	Blocks []richBlockView `json:"blocks"`
	JSON   string          `json:"json"`
}

type richView struct {
	Variants []richVariantView `json:"variants"`
	Model    string            `json:"model"`
}

func (f *folderSettings) richVariants(context.Context, *http.Client, *chatDesk, string, string, string, []string, []string, bool) (richView, error) {
	return richView{}, nil
}

func checkRichJSON(string) error { return nil }
