package desktopconfig

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
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
	// KindGDrive — папка Google Диска: ищется самим Диском через подключение
	// Google, файлы на Мак не скачиваются.
	KindGDrive Kind = "gdrive"
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
	// URL — для сервера и папки Диска. Токен в конфиг не пишется: он в Связке ключей.
	URL string `json:"url,omitempty"`
	// Conn — подключение, через которое источник читается (папка Диска → Google).
	Conn string `json:"conn,omitempty"`
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
		if s.Kind == KindFolder && s.URL != "" && ShareVolume(s.URL) == "" {
			return fmt.Errorf("%w: %s: адрес сетевой папки — smb://сервер/папка или afp://…, без пароля", ErrSourceTarget, s.ID)
		}
	case KindServer:
		if err := CheckServerURL(s.URL); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrSourceTarget, s.ID, err)
		}
	case KindGDrive:
		switch {
		case DriveFolderID(s.URL) == "":
			return fmt.Errorf("%w: %s: нужна ссылка на папку Google Диска (https://drive.google.com/drive/folders/…)", ErrSourceTarget, s.ID)
		case s.Conn == "":
			return fmt.Errorf("%w: %s: папке Диска нужно подключение Google", ErrSourceTarget, s.ID)
		}
	}
	return nil
}

// shareAddr — адрес сетевой папки: схема, сервер, папка; без пароля и
// без символов, которые сломали бы команду подключения.
var shareAddr = regexp.MustCompile(`^(smb|afp)://[^/@:\s"\\]+(:\d+)?/([^"\\\s?#][^"\\?#]*)$`)

// ShareVolume returns where macOS mounts the share at addr (/Volumes/<папка>);
// "" — адрес не сетевой папки или с паролем.
func ShareVolume(addr string) string {
	m := shareAddr.FindStringSubmatch(strings.TrimSpace(addr))
	if m == nil {
		return ""
	}
	first, _, _ := strings.Cut(m[3], "/")
	name, err := url.PathUnescape(first)
	if err != nil || name == "" {
		return ""
	}
	return "/Volumes/" + name
}

var driveFolder = regexp.MustCompile(`^https://drive\.google\.com/drive/(?:u/\d+/)?folders/([\w-]+)(?:[?#].*)?$`)

// DriveFolderID returns the folder id of a Google Drive folder link; "" — не папка Диска.
func DriveFolderID(link string) string {
	if m := driveFolder.FindStringSubmatch(strings.TrimSpace(link)); m != nil {
		return m[1]
	}
	return ""
}

// CheckServerURL пускает https куда угодно, а http — только на этот Mac:
// по открытому http в сеть токен прочтет любой на пути. Запрос и фрагмент
// (адрес скопирован из браузера) — отказ: путь к API к ним не приклеить.
func CheckServerURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return errors.New("нужен адрес вида https://host")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("адрес без ?… и #…: только https://host или https://host/путь")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopback.Host(u.Hostname()) {
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
// ponytail: общая выдача окна и search_catalog MCP читают все каталоги
// списка (sourcelist), а превью записи и счет в окне — только этот, первый.
// Путь: превью по источнику записи.
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
			c.Sources[i].ID = NewID(c.Sources[i].Kind)
		}
	}
}

// NewID returns a fresh "<kind>-<8 hex>". Окно берет его, добавляя сервер: к id
// привязан токен в Связке ключей, и он нужен до сохранения списка.
func NewID(k Kind) string {
	var b [4]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read не возвращает ошибку (Go 1.24+)
	return string(k) + "-" + hex.EncodeToString(b[:])
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
	if c.Server != "" && CheckServerURL(c.Server) == nil {
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
