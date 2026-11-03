// Package loopback holds the one rule for "this address is only on this
// machine": сервер (serve --addr, --pprof) и клиент (адрес kb-сервера в окне)
// спрашивают его одинаково, двум копиям правила не на чем разойтись.
package loopback

import "net"

// Host reports whether host is localhost or a loopback IP (127.0.0.0/8, ::1).
// Пустой хост и 0.0.0.0 — все интерфейсы, то есть сеть.
func Host(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
