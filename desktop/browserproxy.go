package main

import "net/http"

// Модуль «Браузер»: страница-экран под /browser/ в окне и прокси ее запросов
// на kbengine browser serve этого Mac с токеном из файла.

// browserUpstream is where the browser server listens and the token it wants;
// Why — почему его нет, словами для экрана.
type browserUpstream struct{ URL, Token, Why string }

func isBrowserPath(string) bool { return false }

func newBrowserProxy(func() browserUpstream, http.RoundTripper) http.Handler {
	return http.NotFoundHandler()
}
