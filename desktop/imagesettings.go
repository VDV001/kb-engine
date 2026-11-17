package main

import (
	"context"
	"errors"
)

const imageKeyAccount = "images"

type imageSettingsView struct {
	URL    string `json:"url"`
	Model  string `json:"model"`
	HasKey bool   `json:"hasKey"`
}

type ownImage struct {
	Data  string `json:"data"`
	Cost  string `json:"cost"`
	Model string `json:"model"`
}

func (f *folderSettings) imageSettings() imageSettingsView { return imageSettingsView{} }

func (f *folderSettings) setImageSettings(url, model, key string) error { return nil }

func (f *folderSettings) ownInfographic(ctx context.Context, hc interface{}, base, cab, sku, prompt string, draw imageDesk, fetch func(context.Context, string) ([]byte, error)) (ownImage, error) {
	return ownImage{}, errors.New("stub")
}
