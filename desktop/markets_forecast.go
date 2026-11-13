package main

// «Маркеты → Прогноз» (#580): три файла — ниша прошлого года, ниша сейчас,
// свои продажи — дают прогноз своих продаж по дням.

type forecastFile struct {
	File     string `json:"file"`
	Format   string `json:"format"`
	Products int    `json:"products"`
	Days     int    `json:"days"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type forecastDay struct {
	Day   string  `json:"day"` // 2026-11-11
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

type forecastMonth struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
}

type forecastView struct {
	Files    []forecastFile  `json:"files"`
	Days     []forecastDay   `json:"days"`
	LastYear []forecastDay   `json:"lastYear"`
	Months   []forecastMonth `json:"months"`
	Peak     forecastDay     `json:"peak"`
	Growth   float64         `json:"growth"`
	Share    float64         `json:"share"`
}

func forecastFiles(lastYear, nicheNow, own string) (forecastView, error) {
	return forecastView{}, nil
}
