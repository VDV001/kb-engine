//go:build !devtools

package main

// inspectable — меню WebKit и инспектор (main.go). В устанавливаемом окне их нет:
// консоль дала бы любому у незаблокированного Мака привязки Go и открытые в
// «Доступах» секреты (ревью #515). Отладка — just desktop-inspect.
const inspectable = false
