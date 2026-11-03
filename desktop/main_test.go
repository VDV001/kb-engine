package main

import "testing"

func TestBrowserAddr_LoopbackOnly(t *testing.T) {
	// Режим разработки отдает личные файлы без авторизации: наружу его не
	// поднять даже по ошибке в аргументе.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-browser", "127.0.0.1:18092"}, "127.0.0.1:18092"},
		{[]string{"-browser", "0.0.0.0:18092"}, ""},
		{[]string{"-browser", ":18092"}, ""},
		{[]string{"-browser"}, ""},
	} {
		if got := browserAddr(tc.args); got != tc.want {
			t.Errorf("browserAddr(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
