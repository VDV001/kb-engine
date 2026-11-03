package desktopconfig

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Kind — вид источника: откуда окно и MCP берут записи и файлы.
type Kind string

const (
	// KindFolder — папка на этом Mac или смонтированная сетевая (SMB, VPN — путь в /Volumes).
	KindFolder Kind = "folder"
	// KindCatalog — catalog.json базы знаний на диске.
	KindCatalog Kind = "catalog"
	// KindServer — kbengine serve на другой машине (VPS, Yandex Cloud) или на этой.
	KindServer Kind = "server"
)

// Ошибки списка источников: окно показывает текст, вызывающие проверяют errors.Is.
var (
	ErrSourceID     = errors.New("у источника нет id или он повторяется")
	ErrSourceTarget = errors.New("у источника не задан или не подходит путь либо адрес")
)

// Source — один источник. Порядок в Config.Sources — приоритет: при равной
// релевантности выше в выдаче тот, кто выше в списке.
type Source struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// Name — подпись в окне; пусто — окно подписывает путем или адресом.
	Name string `json:"name,omitempty"`
	// Path — для папки и каталога.
	Path string `json:"path,omitempty"`
	// URL — для сервера. Токен в конфиг не пишется: он будет в Связке ключей.
	URL string `json:"url,omitempty"`
	// Enabled — выключенный источник остается в списке со своим местом и
	// настройками, но не читается.
	Enabled bool `json:"enabled"`
}

// check validates the known kinds. Вид, которого эта сборка не знает (его
// записала более новая), не ошибка: источник хранится со своим местом и не
// читается — иначе прежний kbengine-mcp отказал бы во всех папках разом.
func (s Source) check() error {
	switch s.Kind {
	case KindFolder, KindCatalog:
		if strings.TrimSpace(s.Path) == "" {
			return fmt.Errorf("%w: %s без пути", ErrSourceTarget, s.ID)
		}
	case KindServer:
		if err := checkServerURL(s.URL); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrSourceTarget, s.ID, err)
		}
	}
	return nil
}

// checkServerURL пускает https куда угодно, а http — только на этот Mac:
// по открытому http в сеть токен прочтет любой на пути.
func checkServerURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("нужен адрес вида https://host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		return errors.New("http — только для этого Mac, в сеть нужен https")
	default:
		return errors.New("нужен адрес вида https://host")
	}
}

// Validate checks every source and that ids are present and unique.
func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Sources))
	for _, s := range c.Sources {
		if s.ID == "" || seen[s.ID] {
			return fmt.Errorf("%w: %q", ErrSourceID, s.ID)
		}
		seen[s.ID] = true
		if err := s.check(); err != nil {
			return err
		}
	}
	return nil
}

// WithSources returns the config with sources filled in from the fields of the
// shape before sources, if it has none — то же, что видят методы доступа.
func (c Config) WithSources() Config {
	c.migrate()
	return c
}

// list returns the sources, reading a config of the shape before sources
// (roots, catalog) as migrated — так его видят все, кто собрал Config руками,
// а не только Read и Write.
func (c Config) list() []Source {
	c.migrate()
	return c.Sources
}

// FolderRoots returns the enabled folders in priority order.
func (c Config) FolderRoots() []string {
	var out []string
	for _, s := range c.list() {
		if s.Enabled && s.Kind == KindFolder {
			out = append(out, s.Path)
		}
	}
	return out
}

// CatalogPath returns the first enabled catalog, or "" if there is none.
// ponytail: окно и MCP читают один каталог; несколько каталогов начнут
// читаться, когда выдача научится сливать источники (этап 5 плана).
func (c Config) CatalogPath() string {
	for _, s := range c.list() {
		if s.Enabled && s.Kind == KindCatalog {
			return s.Path
		}
	}
	return ""
}

// AssignIDs gives every source without an id "<kind>-<8 hex>" from crypto/rand.
// Счетчик «первый свободный номер» выдал бы id удаленного источника новому, а к
// id привяжется токен сервера в Связке ключей: новый хост получил бы чужой.
func (c *Config) AssignIDs() {
	for i := range c.Sources {
		if c.Sources[i].ID == "" {
			var b [4]byte
			_, _ = rand.Read(b[:]) // crypto/rand.Read не возвращает ошибку (Go 1.24+)
			c.Sources[i].ID = string(c.Sources[i].Kind) + "-" + hex.EncodeToString(b[:])
		}
	}
}

// migrate builds sources from the fields of the config before sources:
// папки по порядку, затем каталог; сервер переезжает выключенным — раньше его
// никто не читал, и окно не должно пойти в сеть просто от обновления.
func (c *Config) migrate() {
	if len(c.Sources) > 0 {
		return
	}
	n := 0
	for _, r := range c.Roots {
		// Пустую строку прежний NormalizeRoots пропускал; переезд не должен
		// превратить ее в источник без пути, который откажет Validate.
		if strings.TrimSpace(r) == "" {
			continue
		}
		n++
		c.Sources = append(c.Sources, Source{ID: "folder-" + strconv.Itoa(n), Kind: KindFolder, Path: r, Enabled: true})
	}
	if c.Catalog != "" {
		c.Sources = append(c.Sources, Source{ID: "catalog-1", Kind: KindCatalog, Path: c.Catalog, Enabled: true})
	}
	// Негодный адрес прежде никого не ронял (его не читали) и не должен
	// уронить окно теперь: такой сервер просто не переезжает.
	if c.Server != "" && checkServerURL(c.Server) == nil {
		c.Sources = append(c.Sources, Source{ID: "server-1", Kind: KindServer, URL: c.Server})
	}
}

// ExpandHome раскрывает «~» и очищает путь, чтобы одна папка в разных записях
// сравнивалась как одна.
func ExpandHome(p, home string) string {
	if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
