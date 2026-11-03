package loopback_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// Одно правило «адрес только на этом Mac» на сервер (serve --addr, --pprof) и
// на клиента (адрес kb-сервера в окне): две копии разошлись бы на первом же
// расширении.
func TestHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "127.8.9.1": true, "::1": true,
		"": false, "0.0.0.0": false, "::": false, "10.8.0.1": false, "192.168.1.5": false,
		"kb.example.ru": false, "localhost.example.ru": false,
	} {
		if got := loopback.Host(host); got != want {
			t.Errorf("Host(%q) = %v, want %v", host, got, want)
		}
	}
}
