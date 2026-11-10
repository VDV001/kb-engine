package cdpbrowser

import (
	_ "embed"
	"encoding/base64"
	"sync"
)

// Стартовая страница браузера: вместо белого экрана. Отдает ее сам сервер, data:-адресом:
// без сети, без отдельного порта и без файла, который можно подменить.
//
//go:embed start.html
var startHTML string

// startAddress — что видит человек в строке адреса вместо длинного data:-адреса.
const startAddress = "dvk://start"

// StartURL is the address of the start page.
var StartURL = sync.OnceValue(func() string {
	return "data:text/html;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(startHTML))
})

// displayURL is the address for the address bar: стартовая страница называется dvk://start.
// Сравнение точное: страница чужого сайта не выдает себя за стартовую.
func displayURL(u string) string {
	if u == StartURL() {
		return startAddress
	}
	return u
}
