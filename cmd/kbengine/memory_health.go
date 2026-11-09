package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// Здоровье памяти (ADR-0008): `memory health` показывает его, `memory review` и
// `memory confirm` его поднимают. Формулировки здесь, в слое показа: домен отдает
// только виды причин и числа.

var healthModeNames = map[domain.TrustMode]string{
	domain.TrustOK: "Доверяю", domain.TrustWarn: "С оговоркой", domain.TrustBad: "Не доверяю",
}

var healthPartNames = map[string]string{
	"rhythm": "Давность осмотра", "queue": "Очередь предложений", "integrity": "Целостность", "rules": "Свежесть правил",
}

// healthReasonText says a reason in words; тут же, что с ней делать.
func healthReasonText(h domain.Health, r domain.HealthReason) string {
	switch r.Kind {
	case domain.ReasonSeal:
		return "HOT.md изменен мимо системы: прочитайте его и подтвердите (kbengine memory seal)"
	case domain.ReasonQueue:
		return fmt.Sprintf("предложений ждет решения: %d, старшее %d дн. (kbengine memory pending)", r.N, h.OldestPendingDays)
	case domain.ReasonRules:
		return fmt.Sprintf("правил без подтверждения дольше %d дней: %d (kbengine memory confirm --rule …)", domain.RuleStaleDays, r.N)
	case domain.ReasonVectors:
		return fmt.Sprintf("векторов отстало от записей: %d (kbengine memory index)", r.N)
	case domain.ReasonRhythm:
		if r.N < 0 {
			return "решений в журнале нет (kbengine memory review)"
		}
		return fmt.Sprintf("осмотра не было %d дн. (kbengine memory review)", r.N)
	}
	return string(r.Kind)
}

type (
	healthPartJSON struct {
		ID    string  `json:"id"`
		Value float64 `json:"value"`
		Max   float64 `json:"max"`
	}
	healthReasonJSON struct {
		Kind string `json:"kind"`
		N    int    `json:"n"`
		Text string `json:"text"`
	}
	healthRuleJSON struct {
		Text    string `json:"text"`
		AgeDays int    `json:"age_days"`
		Stale   bool   `json:"stale"`
	}
	healthJSON struct {
		Mode     string             `json:"mode"`
		Total    int                `json:"total"`
		IdleDays int                `json:"idle_days"`
		Parts    []healthPartJSON   `json:"parts"`
		Reasons  []healthReasonJSON `json:"reasons"`
		Rules    []healthRuleJSON   `json:"rules"`
	}
)

func newHealthJSON(h domain.Health) healthJSON {
	out := healthJSON{Mode: string(h.Mode), Total: h.Total, IdleDays: h.IdleDays}
	for _, p := range h.Parts {
		out.Parts = append(out.Parts, healthPartJSON{p.ID, p.Value, p.Max})
	}
	for _, r := range h.Reasons {
		out.Reasons = append(out.Reasons, healthReasonJSON{string(r.Kind), r.N, healthReasonText(h, r)})
	}
	for _, r := range h.Rules {
		out.Rules = append(out.Rules, healthRuleJSON{r.Text, r.AgeDays, r.Stale})
	}
	return out
}

// memoryHealth: код выхода 1 при «не доверяю», как у check: скрипт видит провал.
func memoryHealth(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory health", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "машинный вид для скриптов")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	h, err := memory.New(memorydir.New(dir), memory.Limits{}).Health()
	if err != nil {
		return memoryFail(stderr, err)
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(newHealthJSON(h)); err != nil {
			return memoryFail(stderr, err)
		}
	} else {
		printHealth(stdout, h)
	}
	if h.Mode == domain.TrustBad {
		return 1
	}
	return 0
}

func printHealth(w io.Writer, h domain.Health) {
	fmt.Fprintf(w, "Доверие: %s — здоровье %d из 100\n", healthModeNames[h.Mode], h.Total)
	for _, p := range h.Parts {
		fmt.Fprintf(w, "  %-20s %2.0f / %.0f\n", healthPartNames[p.ID], p.Value, p.Max)
	}
	if len(h.Reasons) > 0 {
		fmt.Fprintln(w, "Что не так:")
		for _, r := range h.Reasons {
			fmt.Fprintf(w, "  - %s\n", healthReasonText(h, r))
		}
	}
	if len(h.Rules) > 0 {
		fmt.Fprintln(w, "Правила:")
		for _, r := range h.Rules {
			age := "без отметки"
			if r.AgeDays >= 0 {
				age = fmt.Sprintf("%d дн.", r.AgeDays)
			}
			mark := "свежее "
			if r.Stale {
				mark = "устарело"
			}
			fmt.Fprintf(w, "  %s %-11s %s\n", mark, age, r.Text)
		}
	}
	fmt.Fprintln(w, "Формула не откалибрована: веса выбраны, а не измерены (ADR-0008).")
}

// confirmWord — слово, которое вводит человек: осмотр и подтверждение правила
// поднимают доверие, и агент без терминала их не отметит.
const confirmWord = "подтверждаю"

func memoryReview(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if err := memoryConfirm("Вы просмотрели память и ничего менять не нужно? Введите «"+confirmWord+"»: ", confirmWord); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	if err := memory.New(memorydir.New(dir), memory.Limits{}).Review(); err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintln(stdout, "memory: осмотр записан в journal/decisions.md")
	return 0
}

func memoryConfirmRule(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory confirm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rule := fs.String("rule", "", "правило из раздела «Правила работы» HOT.md (регистр и точка не важны)")
	dir, code, stop := memoryDirArg(fs, args, stderr)
	if stop {
		return code
	}
	if *rule == "" {
		fmt.Fprintln(stderr, "memory: укажите правило: --rule \"текст\"")
		return 2
	}
	if err := memoryConfirm("Правило «"+*rule+"» верно и остается? Введите «"+confirmWord+"»: ", confirmWord); err != nil {
		fmt.Fprintf(stderr, "memory: %v\n", err)
		return 1
	}
	err := memory.New(memorydir.New(dir), memory.Limits{}).ConfirmRule(*rule)
	if errors.Is(err, domain.ErrNoSuchRule) {
		fmt.Fprintf(stderr, "memory: в HOT.md нет такого правила: %q\n", *rule)
		return 1
	}
	if err != nil {
		return memoryFail(stderr, err)
	}
	fmt.Fprintln(stdout, "memory: правило подтверждено, запись в journal/decisions.md")
	return 0
}
