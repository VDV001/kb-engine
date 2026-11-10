package desktopconfig

import (
	"path/filepath"
	"strings"
)

// Browser — выбор браузера для модуля «Браузер»: путь к исполняемому файлу из
// списка установленных; пусто — «Автоматически», первый найденный.
type Browser struct {
	Path string `json:"path,omitempty"`
}

// dropBadBrowser снимает выбор, который не путь: окно запускает этот файл, а
// конфиг читает и MCP-сервер, поэтому испорченное значение не должно гасить
// ни то ни другое. Относительные пути и имена без каталога отбрасываются.
func (c *Config) dropBadBrowser() {
	if p := strings.TrimSpace(c.Browser.Path); p == "" || !filepath.IsAbs(p) {
		c.Browser.Path = ""
	}
}
