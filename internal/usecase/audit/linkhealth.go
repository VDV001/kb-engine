package audit

import "github.com/daniil/kb-engine/internal/domain"

// LinkHealth — что база знает про собственные ссылки, одним срезом.
//
// Существует затем же, зачем существует сам скан: до этого результат проверки
// лежал в каталоге и не попадал ни на один экран, то есть база знала про свои
// ссылки больше, чем могла сказать. Правило 11 стандарта работает в обе
// стороны — не только «назови непроверенное», но и «покажи проверенное».
//
// Unchecked стоит рядом с остальными намеренно: доля живых ссылок без числа
// непроверенных читается как утверждение о всей базе, хотя относится только к
// той её части, которую спрашивали.
type LinkHealth struct {
	// Alive — ответили 200. Считаются вычитанием: скан записывает код только
	// для не-200, поэтому у живой ссылки есть дата проверки и нет кода.
	Alive int `json:"alive"`
	// Moved — переехали (3xx). Материал на месте, адрес устарел.
	Moved int `json:"moved"`
	// Gone — 404 и 410, материала нет.
	Gone int `json:"gone"`
	// Undecidable — ответ, из которого ничего не следует. Прежде всего 403:
	// habr отвечает им и на снятую статью, и на бота, которого не стал
	// обслуживать. Записать такие в живые или в мёртвые значило бы соврать.
	Undecidable int `json:"undecidable"`
	// Unchecked — адрес есть, но его ни разу не спрашивали.
	Unchecked int `json:"unchecked"`
	// WithURL — знаменатель: записи, у которых вообще есть что проверять.
	// Собственные стандарты и разборы адреса не имеют и сюда не попадают.
	WithURL int `json:"with_url"`
}

// LinkHealth totals what the last drift scan learned.
func (s *Service) LinkHealth() (LinkHealth, error) {
	c, err := s.loader.Load()
	if err != nil {
		return LinkHealth{}, err
	}

	var h LinkHealth
	for _, e := range c.Entries() {
		if e.URL() == "" {
			continue // нечего спрашивать
		}
		h.WithURL++
		if e.DriftCheckDate() == nil {
			h.Unchecked++
			continue
		}
		code := e.DriftHTTPCode()
		if code == nil {
			h.Alive++ // проверена, кода нет — значит ответила 200
			continue
		}
		// Классификация живёт в домене: тот же переход код → состояние, который
		// применяет сам скан, иначе экран и скан однажды разойдутся в том, что
		// считать мёртвым.
		st, err := domain.ClassifyLinkStatus(*code)
		if err != nil {
			return LinkHealth{}, err
		}
		switch st.String() {
		case "alive":
			h.Alive++
		case "moved":
			h.Moved++
		case "gone":
			h.Gone++
		default:
			h.Undecidable++
		}
	}
	return h, nil
}
