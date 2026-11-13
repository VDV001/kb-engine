package main

// «Маркеты» (#580): экран «Ниша» читает выгрузку ниш из сервиса аналитики.

// nicheTable — прочитанный файл ниш для окна.
type nicheTable struct {
	Path      string         `json:"path"`
	File      string         `json:"file"`
	HeaderRow int            `json:"headerRow"`
	NeedName  bool           `json:"needName"`
	Fields    []nicheField   `json:"fields"`
	Columns   []nicheColumn  `json:"columns"`
	Niches    []nicheRow     `json:"niches"`
	Problems  []nicheProblem `json:"problems"`
}

type nicheField struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type nicheColumn struct {
	Index  int    `json:"index"`
	Header string `json:"header"`
	Field  string `json:"field"`
}

type nicheRow struct {
	Name   string             `json:"name"`
	Values map[string]float64 `json:"values"`
}

type nicheProblem struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// nichePick — выбор человека: колонка и величина в ней, "" — не нужна.
type nichePick struct {
	Index int    `json:"index"`
	Field string `json:"field"`
}

func readNicheFile(path string, picks []nichePick) (nicheTable, error) {
	return nicheTable{}, nil
}
