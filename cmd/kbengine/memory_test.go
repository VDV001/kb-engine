package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/portraitfile"
	"github.com/daniil/kb-engine/internal/domain"
)

func runMem(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"memory"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestMemory_initThenCheckIsGreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	if code, _, e := runMem(t, "init", dir); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if code, out, e := runMem(t, "check", dir); code != 0 {
		t.Fatalf("check: %d %s %s", code, out, e)
	}
	if code, _, _ := runMem(t, "init", dir); code == 0 {
		t.Error("повторный init затер бы память, а вернул 0")
	}
}

func TestMemory_checkNamesTheProblem(t *testing.T) {
	askHuman(t, true)
	dir := t.TempDir()
	runMem(t, "init", dir)
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(strings.Repeat("я", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runMem(t, "check", dir)
	if code != 1 || !strings.Contains(out, "hot-too-big") || !strings.Contains(out, "5000") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	runMem(t, "seal", dir)
	// свой лимит
	if code, _, _ := runMem(t, "check", "--hot-limit", "6000", dir); code != 0 {
		t.Errorf("--hot-limit 6000 должен пускать 5000 знаков, code=%d", code)
	}
}

func TestMemory_addThenCheck(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	code, _, e := runMem(t, "add", "--name", "likes-short", "--topic", "стиль", "--description", "любит коротко", "--body", "текст", dir)
	if code != 0 {
		t.Fatalf("add: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("check после add: %d %s", code, out)
	}
	if code, _, _ := runMem(t, "add", "--name", "Bad Name", "--topic", "стиль", "--description", "x", dir); code != 2 {
		t.Errorf("плохое имя: code=%d, want 2", code)
	}
}

func TestMemory_usage(t *testing.T) {
	if code, _, _ := runMem(t); code != 2 {
		t.Errorf("без подкоманды code=%d, want 2", code)
	}
	if code, _, _ := runMem(t, "nope"); code != 2 {
		t.Errorf("неизвестная подкоманда code=%d, want 2", code)
	}
}

// Без папки в аргументах память живет в dvk-memory/ рядом с настройками окна.
func TestMemory_defaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if code, _, e := runMem(t, "init"); code != 0 {
		t.Fatalf("init без папки: %d %s", code, e)
	}
	def := filepath.Join(home, "Library", "Application Support", "kb-desktop", "dvk-memory")
	for _, f := range []string{"HOT.md", "cold/INDEX.md", "journal"} {
		if _, err := os.Stat(filepath.Join(def, f)); err != nil {
			t.Errorf("нет %s: %v", f, err)
		}
	}
	if code, out, e := runMem(t, "check"); code != 0 {
		t.Errorf("check без папки: %d %s %s", code, out, e)
	}
}

func TestMemory_addNeedsTopic(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if code, _, _ := runMem(t, "add", "--name", "x", "--description", "y", dir); code != 2 {
		t.Errorf("add без --topic: code=%d, want 2", code)
	}
}

// Запись есть, перечня тем в горячем слое нет: check обязан назвать это.
func TestMemory_checkNamesMissingTopicList(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "x", "--topic", "стиль", "--description", "y", dir)
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("ручной горячий слой"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runMem(t, "check", dir)
	if code != 1 || !strings.Contains(out, "no-topic-list") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestMemory_checkIndexLimitFlag(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if code, out, _ := runMem(t, "check", "--index-limit", "5", dir); code != 1 || !strings.Contains(out, "index-too-big") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestMemory_addKeepsTopicListFresh(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	runMem(t, "add", "--name", "b", "--topic", "здоровье", "--description", "про b", dir)
	hot, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if !strings.Contains(string(hot), "- здоровье\n- финансы") {
		t.Fatalf("перечень тем не собран:\n%s", hot)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("check: %d %s", code, out)
	}
}

// Запись положили мимо add: перечень отстал, check называет это, topics чинит.
func TestMemory_topicsFixesStaleList(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	hand := "---\nname: b\ntopic: здоровье\ndescription: про b\n---\n\nтекст\n"
	if err := os.WriteFile(filepath.Join(dir, "cold", "b.md"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "cold", "INDEX.md")
	f, err := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("- [b](b.md) — про b\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMem(t, "check", dir); code != 1 || !strings.Contains(out, "stale-topics") {
		t.Fatalf("check: %d %q", code, out)
	}
	if code, _, e := runMem(t, "topics", dir); code != 0 {
		t.Fatalf("topics: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после topics check красный: %d %s", code, out)
	}
}

func TestMemory_topicsWithoutMarkersLeavesHotAlone(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "a", "--topic", "финансы", "--description", "про a", dir)
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("ручной"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runMem(t, "topics", dir); code != 1 {
		t.Errorf("topics без меток: code=%d, want 1", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "HOT.md")); string(b) != "ручной" {
		t.Errorf("HOT.md затронут: %q", b)
	}
}

func TestMemory_agentQueueEndToEnd(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	code, out, e := runMem(t, "add", "--origin", "agent", "--name", "agent-note", "--topic", "стиль", "--description", "предложение", "--body", "тело", dir)
	if code != 0 || !strings.Contains(out, "ждет одобрения") {
		t.Fatalf("add --origin agent: %d %q %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "cold", "agent-note.md")); err == nil {
		t.Fatal("агент записал прямо в холодный слой")
	}
	if code, out, _ := runMem(t, "pending", dir); code != 0 || !strings.Contains(out, "agent-note") {
		t.Fatalf("pending: %d %q", code, out)
	}
	if code, _, e := runMem(t, "approve", "--name", "agent-note", "--to", "cold", dir); code != 0 {
		t.Fatalf("approve: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после approve check красный: %d %s", code, out)
	}
	if code, out, _ := runMem(t, "pending", dir); !strings.Contains(out, "очередь пуста") || code != 0 {
		t.Errorf("pending после approve: %d %q", code, out)
	}
}

func TestMemory_approveToHotAndReject(t *testing.T) {
	askHuman(t, true)
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--origin", "agent", "--name", "rule-1", "--topic", "стиль", "--description", "не пушить в main", dir)
	runMem(t, "add", "--origin", "agent", "--name", "rule-2", "--topic", "стиль", "--description", "лишнее", dir)
	if code, _, e := runMem(t, "approve", "--name", "rule-1", "--to", "hot", dir); code != 0 {
		t.Fatalf("approve hot: %d %s", code, e)
	}
	hot, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if !strings.Contains(string(hot), "- не пушить в main") {
		t.Errorf("правила нет в HOT.md:\n%s", hot)
	}
	if code, _, e := runMem(t, "reject", "--name", "rule-2", dir); code != 0 {
		t.Fatalf("reject: %d %s", code, e)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "journal", "*rule-*"))
	if len(left) != 2 {
		t.Errorf("в журнале должно быть 2 файла (одобренное и отклоненное), есть %v", left)
	}
	if code, _, _ := runMem(t, "approve", "--name", "rule-1", "--to", "hot", dir); code != 1 {
		t.Errorf("повторное одобрение: code=%d, want 1", code)
	}
	if code, _, _ := runMem(t, "approve", "--name", "x", "--to", "middle", dir); code != 2 {
		t.Errorf("--to middle: code=%d, want 2", code)
	}
}

// embedServer говорит протоколом Ollama: те же две оси, что в тестах сценария.
func embedServer(t *testing.T) *httptest.Server {
	t.Helper()
	money, health := regexp.MustCompile(`(?i)фин|ден|бюдж`), regexp.MustCompile(`(?i)здоров|врач`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Prompt string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{
			float32(len(money.FindAllString(in.Prompt, -1))), float32(len(health.FindAllString(in.Prompt, -1))), 0.1,
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedForFind(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--name", "budget-rules", "--topic", "финансы", "--description", "бюджет и деньги по месяцам", "--body", "Расходы без переводов себе.", dir)
	runMem(t, "add", "--name", "doctor", "--topic", "здоровье", "--description", "визит к врачу раз в год", "--body", "Код PROD-0079.", dir)
	return dir
}

func TestMemory_findTextOutOfTheBox(t *testing.T) {
	dir := seedForFind(t)
	code, out, e := runMem(t, "find", "--mode", "text", "--q", "бюджет", dir)
	if code != 0 || !strings.Contains(out, "budget-rules") || strings.Contains(out, "doctor") {
		t.Fatalf("find: %d %q %s", code, out, e)
	}
	if code, out, _ := runMem(t, "find", "--mode", "text", "--q", "квантовая гравитация", dir); code != 0 || !strings.Contains(out, "ничего не найдено") {
		t.Errorf("пустая выдача: %d %q", code, out)
	}
	if code, _, _ := runMem(t, "find", dir); code != 2 {
		t.Errorf("find без --q: code=%d, want 2", code)
	}
	if code, _, _ := runMem(t, "find", "--mode", "mix", "--q", "x", dir); code != 2 {
		t.Errorf("--mode mix: code=%d, want 2", code)
	}
}

func TestMemory_indexThenFindByMeaning(t *testing.T) {
	dir := seedForFind(t)
	srv := embedServer(t)
	if code, out, e := runMem(t, "index", "--embed-url", srv.URL, dir); code != 0 || !strings.Contains(out, "2") {
		t.Fatalf("index: %d %q %s", code, out, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "cold", ".vectors.json")); err != nil {
		t.Fatalf("индекса нет: %v", err)
	}
	code, out, _ := runMem(t, "find", "--embed-url", srv.URL, "--q", "сколько денег уходит", dir)
	if code != 0 || !strings.Contains(out, "vector") || !strings.Contains(out, "budget-rules") {
		t.Fatalf("find по смыслу: %d %q", code, out)
	}
	// записали новое мимо индекса: check называет, index чинит
	runMem(t, "add", "--name", "extra", "--topic", "финансы", "--description", "еще про деньги", dir)
	if code, out, _ := runMem(t, "check", dir); code != 1 || !strings.Contains(out, "stale-vectors") {
		t.Fatalf("check: %d %q", code, out)
	}
	runMem(t, "index", "--embed-url", srv.URL, dir)
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после index check красный: %d %q", code, out)
	}
}

// Служба эмбеддингов недоступна: поиск не молчит и не падает.
func TestMemory_findFallsBackWhenEmbedServiceIsDown(t *testing.T) {
	dir := seedForFind(t)
	code, out, _ := runMem(t, "find", "--embed-url", "http://127.0.0.1:1/api/embeddings", "--q", "бюджет", dir)
	if code != 0 || !strings.Contains(out, "budget-rules") {
		t.Fatalf("find: %d %q", code, out)
	}
}

func TestMemory_indexNeedsAnEmbedService(t *testing.T) {
	dir := seedForFind(t)
	if code, _, _ := runMem(t, "index", "--embed-url", "http://127.0.0.1:1/api/embeddings", dir); code != 1 {
		t.Errorf("недоступная служба: code=%d, want 1", code)
	}
}

// seedPortrait кладет в папку портрета выдуманный портрет: один одобренный факт,
// один ждущий одобрения (его в HOT.md быть не должно).
func seedPortrait(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	ok, err := domain.NewFact("f1", domain.SectionAbout, "Разработчик Го", domain.OriginManual, domain.FactApproved, at)
	if err != nil {
		t.Fatal(err)
	}
	wait, err := domain.NewFact("f2", domain.SectionMisc, "Любит чай", domain.OriginChat, domain.FactPending, at)
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.NewPortrait([]domain.Fact{ok, wait})
	if err != nil {
		t.Fatal(err)
	}
	if err := (portraitfile.Store{Dir: dir}).Save(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMemory_whoFillsHotFromThePortrait(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := t.TempDir()
	runMem(t, "init", mem)
	code, out, e := runMem(t, "who", "--portrait-dir", seedPortrait(t), mem)
	if code != 0 {
		t.Fatalf("who: %d %q %s", code, out, e)
	}
	hot, _ := os.ReadFile(filepath.Join(mem, "HOT.md"))
	if !strings.Contains(string(hot), "Обо мне: Разработчик Го.") {
		t.Errorf("факта нет в HOT.md:\n%s", hot)
	}
	if strings.Contains(string(hot), "Любит чай") {
		t.Error("факт, не одобренный человеком, попал в HOT.md")
	}
	if code, out, _ := runMem(t, "who", "--portrait-dir", seedPortrait(t), mem); code != 0 || !strings.Contains(out, "актуален") {
		t.Errorf("повторный who: %d %q", code, out)
	}
	if code, out, _ := runMem(t, "check", mem); code != 0 {
		t.Errorf("после who check красный: %d %q", code, out)
	}
}

func TestMemory_whoNeedsAPortraitFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // настроек окна нет
	mem := t.TempDir()
	runMem(t, "init", mem)
	if code, _, e := runMem(t, "who", mem); code != 1 || !strings.Contains(e, "портрет") {
		t.Errorf("who без папки: code=%d err=%q", code, e)
	}
}

// Потолок задается при init (ADR-0008) и дальше живет в memory.json: окно и
// терминал меряют одной линейкой.
func TestMemory_initHotLimitIsRememberedByCheck(t *testing.T) {
	askHuman(t, true)
	dir := filepath.Join(t.TempDir(), "mem")
	if code, _, e := runMem(t, "init", "--hot-limit", "6000", dir); code != 0 {
		t.Fatalf("init: %d %s", code, e)
	}
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte(strings.Repeat("я", 5000)), 0o600); err != nil {
		t.Fatal(err)
	}
	runMem(t, "seal", dir) // правка руками подтверждена
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("потолок 6000 из init должен пускать 5000 знаков: %d %q", code, out)
	}
	if code, out, _ := runMem(t, "check", "--hot-limit", "4000", dir); code != 1 || !strings.Contains(out, "hot-too-big") {
		t.Errorf("флаг сильнее файла: %d %q", code, out)
	}
	if code, _, _ := runMem(t, "init", "--hot-limit", "7", filepath.Join(t.TempDir(), "m")); code != 2 {
		t.Errorf("негодный потолок: code=%d, want 2", code)
	}
}

// askHuman подменяет подтверждение в терминале: ok — человек ввел имя верно.
func askHuman(t *testing.T, ok bool) {
	t.Helper()
	prev := memoryConfirm
	memoryConfirm = func(prompt, want string) error {
		if !ok {
			return errNotConfirmed
		}
		return nil
	}
	t.Cleanup(func() { memoryConfirm = prev })
}

func TestMemory_sealWorkflow(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Fatalf("свежая память: %d %q", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "HOT.md"), []byte("правка мимо системы"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMem(t, "check", dir); code != 1 || !strings.Contains(out, "hot-changed-outside") {
		t.Fatalf("правка мимо системы не названа: %d %q", code, out)
	}
	askHuman(t, true)
	if code, _, e := runMem(t, "seal", dir); code != 0 {
		t.Fatalf("seal: %d %s", code, e)
	}
	if code, out, _ := runMem(t, "check", dir); code != 0 {
		t.Errorf("после seal: %d %q", code, out)
	}
}

// Запись в горячий слой из терминала — только с жестом человека: имя записи
// вводится руками. Агент без терминала и с чужим именем не пройдет.
func TestMemory_hotWritesNeedAHuman(t *testing.T) {
	dir := t.TempDir()
	runMem(t, "init", dir)
	runMem(t, "add", "--origin", "agent", "--name", "rule-1", "--topic", "стиль", "--description", "не пушить в main", dir)
	askHuman(t, false)
	if code, _, e := runMem(t, "approve", "--name", "rule-1", "--to", "hot", dir); code != 1 || !strings.Contains(e, "подтвержд") {
		t.Fatalf("без подтверждения: code=%d err=%q", code, e)
	}
	if code, _, _ := runMem(t, "seal", dir); code != 1 {
		t.Errorf("seal без подтверждения: code=%d", code)
	}
	hot, _ := os.ReadFile(filepath.Join(dir, "HOT.md"))
	if strings.Contains(string(hot), "не пушить в main") {
		t.Error("правило попало в HOT.md без подтверждения")
	}
	if _, out, _ := runMem(t, "pending", dir); !strings.Contains(out, "rule-1") {
		t.Error("запись пропала из очереди")
	}
}

// Настоящее подтверждение: stdin не терминал (так запускает агент) — отказ.
func TestConfirmOnTerminal_refusesNonTerminals(t *testing.T) {
	if err := confirmOnTerminal(strings.NewReader("rule-1\n"), &bytes.Buffer{})("Введите имя", "rule-1"); err == nil {
		t.Error("ввод не из терминала должен отвергаться")
	}
}
