package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/archmap"
)

// MapsLoader supplies the architecture maps the engine was pointed at. nil
// means none were configured, which is a smaller dashboard rather than an
// error. Called per request, like the catalog and the changelog: a map edited
// while the dashboard is open shows up on reload.
type MapsLoader func() ([]archmap.Map, error)

// mapIndexEntry — карта в оглавлении: чем она себя называет и какого объёма.
// Сценарии и узлы сюда не входят намеренно — живые карты весят по сотне с
// лишним килобайт, а на вкладку заходят сначала выбрать карту.
type mapIndexEntry struct {
	ID        string        `json:"id"`
	Project   string        `json:"project"`
	Commit    string        `json:"commit,omitempty"`
	CheckedAt string        `json:"checked_at,omitempty"`
	Zones     []string      `json:"zones"`
	Stats     archmap.Stats `json:"stats"`
	// Accepted — сколько зон имеют запись о приёмке. Ноль при непустых зонах
	// значит «сверять не с чем», и это не то же самое, что «не принято».
	Accepted int `json:"accepted_zones"`
}

func handleMaps(load MapsLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		maps, err := loadMaps(load)
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]mapIndexEntry, 0, len(maps))
		for _, m := range maps {
			e := mapIndexEntry{
				ID: m.ID, Project: m.Project, Commit: m.Commit,
				CheckedAt: m.CheckedAt, Stats: m.Stats,
				Zones: make([]string, 0, len(m.Zones)),
			}
			for _, z := range m.Zones {
				e.Zones = append(e.Zones, z.Name)
				if z.Accepted {
					e.Accepted++
				}
			}
			out = append(out, e)
		}
		writeJSON(w, struct {
			Maps []mapIndexEntry `json:"maps"`
		}{out})
	}
}

// handleMap serves one map whole.
//
// Отказ называет адреса, которые движок знает: карты подключают флагом, и
// вопрос в этот момент ровно один — какие из них передали. «404» без списка на
// него не отвечает, а пустой список отвечает точнее всего: не передали ни одной.
func handleMap(load MapsLoader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		maps, err := loadMaps(load)
		if err != nil {
			writeError(w, err)
			return
		}
		id := r.PathValue("id")
		for _, m := range maps {
			if m.ID == id {
				writeJSON(w, m)
				return
			}
		}
		known := make([]string, 0, len(maps))
		for _, m := range maps {
			known = append(known, m.ID)
		}
		msg := fmt.Sprintf("no architecture map %q; connected: %s", id, strings.Join(known, ", "))
		if len(known) == 0 {
			msg = fmt.Sprintf("no architecture map %q: none is connected — pass --maps", id)
		}
		http.Error(w, msg, http.StatusNotFound)
	}
}

func loadMaps(load MapsLoader) ([]archmap.Map, error) {
	if load == nil {
		return nil, nil
	}
	return load()
}
