package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"time"
)

// pprofHandler собирает профилировщик на СВОЁМ mux.
//
// `net/http/pprof` регистрирует свои маршруты в http.DefaultServeMux одним
// фактом импорта, и в проекте, где витрина ходит наружу, это худший из
// возможных умолчаний: дампы кучи и стеки всех горутин уехали бы вместе со
// страницей. Поэтому маршруты выписываются руками, а слушатель отдельный.
func pprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// startPprof поднимает профилировщик, если адрес задан, и говорит об этом.
//
// Пустой адрес — выключено: молчаливое умолчание вида «а вдруг пригодится»
// здесь означало бы открытый порт у каждого, кто просто запустил витрину.
func startPprof(addr string, stdout, stderr io.Writer) {
	if addr == "" {
		return
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           pprofHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(stdout, "kbengine: pprof on http://%s/debug/pprof/ (profiles only, separate from the dashboard)\n", addr)
	go func() {
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintf(stderr, "serve: pprof: %v\n", err)
		}
	}()
}
