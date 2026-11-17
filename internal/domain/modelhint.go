package domain

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// ModelRole is which of the two chat models a question goes to (#678).
type ModelRole string

// Роли: главная — для сложного, вторая — для короткого и простого.
const (
	RoleMain   ModelRole = "main"
	RoleSecond ModelRole = "second"
)

// HintReason is why the hint suggests a role: код, а не текст — текст сообщения
// у окна.
type HintReason string

// Причины совета.
const (
	ReasonShort    HintReason = "short"
	ReasonLong     HintReason = "long"
	ReasonCode     HintReason = "code"
	ReasonAnalysis HintReason = "analysis"
	ReasonFiles    HintReason = "files"
)

// ModelHint is the advice for a question; пустая роль — совета нет.
type ModelHint struct {
	Role   ModelRole
	Reason HintReason
}

// longQuestion — с этого числа знаков вопрос считается длинным.
const longQuestion = 400

// analysisWords — основы слов вопросов, где нужна основная модель.
// ponytail: простые подстроки — ложные срабатывания возможны; потолок — тонкая
// классификация; путь вверх — вызов дешевой модели для подсказки (#678).
var analysisWords = []string{"проанализ", "сравни", "подробн", "составь план", "архитектур", "риск", "обоснуй", "разбери", "спроектируй", "рефактор", "оцени"}

// SuggestModelRole advises a model for the question by rules, без вызова
// модели: подсказка бесплатна. files — сколько файлов приложено к вопросу.
func SuggestModelRole(question string, files int) ModelHint {
	q := strings.ToLower(strings.TrimSpace(question))
	switch {
	case q == "":
		return ModelHint{}
	case files > 0:
		return ModelHint{RoleMain, ReasonFiles}
	case strings.Contains(q, "```"):
		return ModelHint{RoleMain, ReasonCode}
	case utf8.RuneCountInString(q) > longQuestion:
		return ModelHint{RoleMain, ReasonLong}
	case slices.ContainsFunc(analysisWords, func(w string) bool { return strings.Contains(q, w) }):
		return ModelHint{RoleMain, ReasonAnalysis}
	}
	return ModelHint{RoleSecond, ReasonShort}
}
