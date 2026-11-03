//go:build windows

package main

import "os"

// tokenFileMode on Windows checks nothing: биты прав там ничего не значат (Go
// отдает 0666 или 0444), а доступ решают ACL. Проверка 0600 не проходила бы
// никогда, и --token-file был бы недоступен.
//
// ponytail: доступ к файлу токена на Windows не проверяется. Потолок: токен,
// который читают другие пользователи машины. Путь: проверка ACL через
// golang.org/x/sys/windows, когда serve понадобится на Windows.
func tokenFileMode(os.FileInfo) error { return nil }
