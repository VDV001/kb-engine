package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/kbserver"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/adapter/sourcelist"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/search"
)

// remoteTimeout — сколько ждать сервер и, отдельно, Связку ключей: агент не
// должен висеть на хосте без VPN.
const remoteTimeout = 3 * time.Second

// tokenGetter reads a server's token: Связка ключей, в тестах — память.
type tokenGetter interface {
	Get(account string) (string, error)
}

// remoteServers is mcpserver.RemoteSearcher over the window's source list
// (--files-config): те же каталоги и серверы в том же порядке, тот же токен в
// Связке, что у окна, — агент видит то, что видит человек. Выключенный в окне
// источник не спрашивается, включая свой каталог.
//
// Адрес и токен читаются одной парой под замком config.json, тем же, под
// которым окно меняет Связку и конфиг (desktop SetSources): иначе поиск в
// момент сохранения прочел бы старый адрес и новый токен, и токен ушел бы
// старому хосту. Сеть — уже без замка: сохранение в окне не ждет серверы.
//
// ponytail: токен читается из Связки на каждый вызов, без кэша окна
// (cachedTokens). Потолок — запуск security на каждый поиск агента; путь —
// кэш с поколением, как в окне, если агент начнет искать чаще человека.
type remoteServers struct {
	cfgPath string
	home    string
	// ownCatalog — раскрытый путь --catalog: этот каталог списка ищет сам
	// mcpserver (Own), тем же поиском, что get_entry и stats.
	ownCatalog string
	tokens     tokenGetter
}

// windowMatch — чем окно ищет в каталогах списка (desktop searchCatalog): без
// словаря синонимов своего каталога. Вторые каталоги агент видит так же.
var windowMatch = search.Matcher{}

// SearchRemote asks every enabled source of the list in order, in parallel.
// Конфиг читается на каждый вызов: окно меняет список без перезапуска MCP.
// Список не прочитан — свой каталог отвечает, как до списка источников, и беда
// названа строкой config.
func (r remoteServers) SearchRemote(ctx context.Context, q string) []mcpserver.RemoteFound {
	rows, err := r.snapshot(ctx, q)
	ownOnly := func(msg string) []mcpserver.RemoteFound {
		return []mcpserver.RemoteFound{{Own: true}, {Source: mcpserver.RemoteSource{ID: "config"}, Err: msg}}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return nil // агент отменил запрос: ответ уже никто не читает
	case errors.Is(err, filelock.ErrBusy):
		return ownOnly("окно сохраняет источники, серверы не спрошены: повторите поиск")
	case err != nil:
		return ownOnly("конфиг окна не прочитался: " + err.Error())
	}
	out := make([]mcpserver.RemoteFound, len(rows))
	var wg sync.WaitGroup
	for i, row := range rows {
		wg.Go(func() {
			if row.src.Kind == desktopconfig.KindServer {
				out[i] = r.ask(ctx, row, q)
				return
			}
			out[i] = r.catalog(row.src, q)
		})
	}
	wg.Wait()
	return out
}

// catalog answers one catalog of the list: свой — строкой Own, другой —
// найденным в его файле тем же поиском, что у своего.
func (r remoteServers) catalog(s desktopconfig.Source, q string) mcpserver.RemoteFound {
	found := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: s.ID, Name: s.Name}}
	path := filepath.Clean(desktopconfig.ExpandHome(s.Path, r.home))
	if samePath(path, r.ownCatalog) {
		found.Own = true
		return found
	}
	// ponytail: файл второго каталога читается на каждый вызов, как и в окне
	// (desktop searchCatalog). Потолок — разбор всего файла на каждый поиск
	// агента; путь — кэш по (путь, mtime, размер), общий с окном.
	entries, err := sourcelist.SearchCatalog(path, q, windowMatch)
	if err != nil {
		found.Err = "каталог не прочитался: " + err.Error()
		return found
	}
	found.Catalog = append([]domain.Entry{}, entries...) // не nil: каталог, а не сервер
	return found
}

// samePath reports whether two paths name one file, following symlinks: окно
// хранит путь, каким его выбрал человек, MCP запущен с другим.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// withToken is an enabled source of the list; у сервера — что Связка дала
// для него.
type withToken struct {
	src desktopconfig.Source
	tok string
	err error
}

// snapshot reads the enabled catalogs and servers, and the servers' tokens,
// under the config lock, waiting for it no longer than remoteTimeout. У
// ожидания замка свой срок, у Связки свой (token): окно, державшее замок почти
// весь срок, не превращает разблокированную Связку в «не ответила».
func (r remoteServers) snapshot(ctx context.Context, q string) ([]withToken, error) {
	waitCtx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	var out []withToken
	err := filelock.WithContext(waitCtx, r.cfgPath, func() error {
		cfg, err := desktopconfig.Read(r.cfgPath)
		if err != nil {
			return err
		}
		for _, s := range sourcelist.Enabled(cfg) {
			// Пустой запрос серверы не спрашивает: им нечего искать, а весь
			// каталог агент просит у каталогов.
			if (s.Kind == desktopconfig.KindServer && q != "") || s.Kind == desktopconfig.KindCatalog {
				out = append(out, withToken{src: s})
			}
		}
		var wg sync.WaitGroup
		for i := range out {
			if out[i].src.Kind == desktopconfig.KindServer {
				wg.Go(func() { out[i].tok, out[i].err = r.token(ctx, out[i].src.ID) })
			}
		}
		wg.Wait()
		return nil
	})
	return out, err
}

// ask searches one server with the token read in its snapshot.
func (r remoteServers) ask(ctx context.Context, w withToken, q string) mcpserver.RemoteFound {
	s, tok := w.src, w.tok
	found := mcpserver.RemoteFound{Source: mcpserver.RemoteSource{ID: s.ID, Name: s.Name, URL: s.URL}}
	if w.err != nil && !errors.Is(w.err, keychain.ErrNotFound) {
		found.Err = "Связка ключей не ответила: разблокируйте ее"
		return found
	}
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	res, err := kbserver.Client{URL: s.URL, Token: tok}.Search(ctx, q, mcpserver.RemoteLimit)
	if err != nil {
		found.Err = kbserver.Describe(err)
		return found
	}
	found.Entries, found.Keys = res.Entries, res.Keys
	return found
}

// token reads the token within remoteTimeout: Связка, ждущая разблокировки,
// не держит ответ агенту.
func (r remoteServers) token(ctx context.Context, id string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	return sourcelist.TokenWithin(ctx, r.tokens.Get, id)
}
