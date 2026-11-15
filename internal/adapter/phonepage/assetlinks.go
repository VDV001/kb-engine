package phonepage

import "net/http"

// AssetLinks serves /.well-known/assetlinks.json for the Android app.
func AssetLinks(string) http.Handler { return http.NotFoundHandler() }
