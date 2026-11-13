package main

import (
	"context"
	"net/http"
	"time"
)

// AddOzon is not written yet.
func (f *folderSettings) AddOzon(name, clientID, key string) (connRow, error) { return connRow{}, nil }

// ozonShop is not written yet.
func (f *folderSettings) ozonShop(ctx context.Context, base string, hc *http.Client, id string, now time.Time) (shopView, error) {
	return shopView{}, nil
}
