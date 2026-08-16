package httpapi

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// kbFilesHandler отдаёт собственные артефакты базы — шпаргалки, курсы, разборы,
// страницы проектов, — те самые, что до сих пор существовали строкой в каталоге
// и не открывались из витрины ничем. Замер на живой базе: 104 записи несут
// file и не несут url, то есть путь к ним был, а способа открыть — нет.
//
// Это НЕ файловый сервер поверх дерева базы, и разница здесь главная. Рядом с
// каталогом лежат личные заметки, черновики и финансы. Маршрут отдаёт файл
// только тогда, когда его путь стоит в поле file какой-нибудь записи каталога:
// чего каталог не называет, того для маршрута не существует.
//
// Из этого же следует, что обход вверх по дереву невозможен не потому, что путь
// чистится, а потому что сравнение идёт со СПИСКОМ: "../.." в поле file не
// стоит и стоять не может. Приведение пути ниже — не защита, а нормализация,
// чтобы "notes/./x.md" и "notes/x.md" считались одним и тем же именем.
//
// Список читается на каждый запрос, а не запоминается при старте: каталог
// движок и так перечитывает на каждый запрос, и запись, добавленная минуту
// назад, должна открываться без перезапуска.
func kbFilesHandler(fsys fs.FS, q Querier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fsys == nil {
			// Источник не подключён — это меньшая база, а не ошибка.
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/kb/")
		if rel == "" || !fs.ValidPath(rel) {
			http.NotFound(w, r)
			return
		}
		allowed, err := catalogNamesFile(q, rel)
		if err != nil {
			http.Error(w, "catalog: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		f, err := fsys.Open(rel)
		if err != nil {
			// Путь каталог называет, а файла нет — запись про артефакт, который
			// переименовали или не написали. Сервер при этом исправен.
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		rs, ok := f.(io.ReadSeeker)
		if !ok {
			http.Error(w, "artefact is not seekable", http.StatusInternalServerError)
			return
		}
		// no-cache, а не immutable: у артефакта нет хеша в имени, владелец
		// правит его под тем же путём, и immutable означал бы, что правку
		// никогда не увидят.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, path.Base(rel), st.ModTime(), rs)
	})
}

// catalogNamesFile отвечает, называет ли каталог этот путь. Сравнение точное:
// поле file записи — это имя артефакта, а не префикс директории, и разрешать
// «всё под этой папкой» значило бы вернуться к файловому серверу.
func catalogNamesFile(q Querier, rel string) (bool, error) {
	entries, err := q.Entries()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if f := e.NotesFile(); f != "" && path.Clean(f) == rel {
			return true, nil
		}
	}
	return false, nil
}
