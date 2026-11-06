package google

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// File is one Drive search hit.
type File struct {
	ID, Name, MimeType, Link string
	Modified                 time.Time
}

// searchLimit — сколько находок Drive отдает за запрос: столько же, сколько
// окно показывает от одного источника.
const searchLimit = "40"

// Search finds q in the documents of folder (full text, Drive's own index).
//
// ponytail: только прямые дети папки — запрос Drive не умеет «в глубину».
// Потолок: документ во вложенной папке не находится. Путь: собрать id
// вложенных папок обходом и искать по «'a' in parents or 'b' in parents».
func (c Client) Search(ctx context.Context, at, folder, q string) ([]File, error) {
	esc := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	query := "'" + esc.Replace(folder) + "' in parents and trashed = false and fullText contains '" + esc.Replace(q) + "'"
	var res struct {
		Files []struct {
			ID           string    `json:"id"`
			Name         string    `json:"name"`
			MimeType     string    `json:"mimeType"`
			WebViewLink  string    `json:"webViewLink"`
			ModifiedTime time.Time `json:"modifiedTime"`
		} `json:"files"`
	}
	err := c.getJSON(ctx, at, "/files", url.Values{
		"q": {query}, "pageSize": {searchLimit}, "fields": {"files(id,name,mimeType,webViewLink,modifiedTime)"},
		"supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"},
	}, &res)
	if err != nil {
		return nil, err
	}
	out := make([]File, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, File{ID: f.ID, Name: f.Name, MimeType: f.MimeType, Link: f.WebViewLink, Modified: f.ModifiedTime})
	}
	return out, nil
}
