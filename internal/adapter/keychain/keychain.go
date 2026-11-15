// Package keychain keeps secrets (the token of a kb server source) in the
// system store without cgo and dependencies: на macOS — Связка ключей через
// системный security (keychain_security.go), на Windows — Диспетчер учетных
// данных через advapi32 (keychain_windows.go).
//
// Токен в config.json лежал бы рядом с путями к личным папкам в файле, который
// копируют в бэкапы; Связка ключей шифрует его и отдает только этому пользователю.
package keychain

// Store is one service in a keychain: Service — имя приложения в Связке
// ключей, Keychain — путь к связке, пусто — login-связка пользователя.
type Store struct {
	Service  string
	Keychain string
}

// safe — для учетки: печатный ASCII без кавычки и обратной косой — %q для
// security -i записал бы не-ASCII как \uXXXX, а security такие escape не
// раскрывает. Секрет идет hex-ом и этой проверки не требует.
func safe(v string) bool {
	if v == "" {
		return false
	}
	for i := range len(v) {
		if c := v[i]; c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}
