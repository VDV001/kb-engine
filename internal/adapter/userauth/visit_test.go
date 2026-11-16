package userauth_test

import (
	"cmp"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/blob"
	"github.com/daniil/kb-engine/internal/adapter/userauth"
	"github.com/daniil/kb-engine/internal/domain"
)

// seenStore counts and can break writes of the visits files.
type seenStore struct {
	blob.Store
	key string // файл входов, за которым следим

	mu      sync.Mutex
	puts    int
	failPut error         // не nil — Put файла входов отказывает
	failGet error         // не nil — Get файла входов отказывает
	hold    chan struct{} // не nil — Put файла входов ждет, пока закроют

	// Только первый Put файла входов: ждет holdFirst и отказывает failFirst.
	holdFirst chan struct{}
	failFirst error
}

func (s *seenStore) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if key == s.key {
		s.mu.Lock()
		s.puts++
		fail, hold := s.failPut, s.hold
		if s.puts == 1 {
			fail, hold = cmp.Or(s.failFirst, fail), cmp.Or(s.holdFirst, hold)
		}
		s.mu.Unlock()
		if hold != nil {
			<-hold
		}
		if fail != nil {
			return "", fail
		}
	}
	return s.Store.Put(ctx, key, data, ifMatch)
}

func (s *seenStore) Get(ctx context.Context, key string) (blob.Object, error) {
	s.mu.Lock()
	fail := s.failGet
	s.mu.Unlock()
	if key == s.key && fail != nil {
		return blob.Object{}, fail
	}
	return s.Store.Get(ctx, key)
}

func (s *seenStore) writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puts
}

// visitsFixture is a person with two phones.
func visitsFixture(t *testing.T) (*seenStore, domain.UserID, string, string) {
	t.Helper()
	ctx := context.Background()
	owner := id(t, "owner")
	s := &seenStore{Store: newStore(t), key: userauth.SeenKey(owner)}
	if _, err := userauth.Add(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	dev, _, err := userauth.AddDevice(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	quiet, _, err := userauth.AddDevice(ctx, s, owner)
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, dev, quiet
}

var t0 = time.Date(2026, 11, 16, 12, 6, 0, 0, time.UTC)

// Окно видит, что телефон вошел и как он себя зовет; запись — не на каждый
// опрос, а раз в VisitEvery и при смене имени (#622). Файл входов у каждого
// человека свой: телефоны разных людей не спорят за один объект (ревью #652).
func TestVisits(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, quiet := visitsFixture(t)
	v := &userauth.Visits{Store: s}

	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Duration{10 * time.Second, time.Minute} {
		if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.writes(); n != 1 {
		t.Fatalf("visits written %d times within VisitEvery, want 1", n)
	}
	_ = v.Visit(ctx, owner, dev, "iPhone 17", t0.Add(2*time.Minute))           // имя с моделью вместо «iPhone» — сразу
	_ = v.Visit(ctx, owner, dev, domain.PhoneIPadOrMac, t0.Add(3*time.Minute)) // другое имя без модели — не сразу
	_ = v.Visit(ctx, owner, dev, domain.PhoneIPadOrMac, t0.Add(userauth.VisitEvery+3*time.Minute))
	if n := s.writes(); n != 3 {
		t.Fatalf("visits written %d times, want 3 (first, the model, after VisitEvery)", n)
	}
	seen, err := v.Seen(ctx, owner)
	if err != nil || len(seen) != 1 {
		t.Fatalf("Seen = %+v, %v", seen, err)
	}
	if got := seen[dev]; got.Name != "iPhone 17" || !got.At.Equal(t0.Add(userauth.VisitEvery+3*time.Minute)) {
		t.Errorf("visited phone %+v", got)
	}
	if _, ok := seen[quiet]; ok {
		t.Errorf("phone that never came in is seen")
	}
}

// «Android» без модели не затирает «Samsung SM-S918B» и не пишется сразу —
// иначе имя прыгает и предел VisitEvery обходится (ревью #652).
func TestVisitsKeepTheBetterName(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	_ = v.Visit(ctx, owner, dev, "Samsung SM-S918B", t0)
	_ = v.Visit(ctx, owner, dev, domain.PhoneAndroid, t0.Add(10*time.Second))
	if n := s.writes(); n != 1 {
		t.Fatalf("visits written %d times, want 1", n)
	}
	// Новый процесс сервера: кэша нет, ранжирует по записанному.
	fresh := &userauth.Visits{Store: s}
	_ = fresh.Visit(ctx, owner, dev, domain.PhoneAndroid, t0.Add(time.Minute))
	seen, _ := fresh.Seen(ctx, owner)
	if got := seen[dev]; got.Name != "Samsung SM-S918B" || !got.At.Equal(t0.Add(time.Minute)) {
		t.Fatalf("after a visit without the model: %+v", got)
	}
}

// Сбой записи — пауза VisitRetry для всех телефонов процесса: сломанный бакет
// не получает запрос и строку журнала на каждый опрос (ревью #652).
func TestVisitsPauseAfterFailure(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, quiet := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	s.failPut = errors.New("bucket down")
	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0); err == nil {
		t.Fatal("failed write reported no error")
	}
	for _, d := range []string{dev, quiet} {
		if err := v.Visit(ctx, owner, d, domain.PhoneIPhone, t0.Add(10*time.Second)); err != nil {
			t.Fatalf("visit during the pause: %v", err)
		}
	}
	if n := s.writes(); n != 1 {
		t.Fatalf("visits written %d times during the pause, want 1", n)
	}
	s.mu.Lock()
	s.failPut = nil
	s.mu.Unlock()
	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0.Add(userauth.VisitRetry+time.Second)); err != nil {
		t.Fatal(err)
	}
	if seen, _ := v.Seen(ctx, owner); seen[dev].At.IsZero() {
		t.Fatal("visit after the pause not written")
	}
}

// Два одновременных опроса одного телефона пишут один раз (ревью #652).
func TestVisitsConcurrentPollsWriteOnce(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	s.hold = make(chan struct{})
	first := make(chan error)
	go func() { first <- v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0) }()
	for s.writes() == 0 {
		time.Sleep(time.Millisecond)
	}
	second := make(chan error, 1)
	go func() { second <- v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0.Add(time.Second)) }()
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second): // второй опрос пишет тоже и ждет ту же запись
		close(s.hold)
		<-first
		<-second
		t.Fatalf("second poll of the same phone writes too: %d writes", s.writes())
	}
	close(s.hold)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := s.writes(); n != 1 {
		t.Fatalf("visits written %d times, want 1", n)
	}
}

// Отвязанный телефон и удаленный человек не оставляют входов: опрос, что шел
// во время отвязки, не дописывает телефон обратно (ревью #652).
func TestVisitsForgetRevoked(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, quiet := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	_ = v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0)
	_ = v.Visit(ctx, owner, quiet, domain.PhoneAndroid, t0)
	if err := userauth.RevokeDevice(ctx, s, owner, dev); err != nil {
		t.Fatal(err)
	}
	// Опрос с токеном, что был проверен до отвязки.
	late := &userauth.Visits{Store: s}
	if err := late.Visit(ctx, owner, dev, domain.PhoneIPhone, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	seen, err := v.Seen(ctx, owner)
	if _, ok := seen[dev]; ok || err != nil || len(seen) != 1 {
		t.Fatalf("after revoke: %+v, %v", seen, err)
	}
	if err := userauth.Revoke(ctx, s, owner); err != nil {
		t.Fatal(err)
	}
	if seen, err := v.Seen(ctx, owner); err != nil || len(seen) != 0 {
		t.Fatalf("after the person is removed: %+v, %v", seen, err)
	}
	o, err := s.Store.Get(ctx, userauth.SeenKey(owner))
	if err == nil && len(o.Data) > 20 {
		t.Fatalf("visits file kept after the person is removed: %s", o.Data)
	}
}

// Испорченный файл входов — подсказка окну, а не замок: следующий вход
// пишет его заново, а не падает на каждом опросе (ревью #652).
func TestVisitsHealCorruptFile(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	if _, err := s.Store.Put(ctx, userauth.SeenKey(owner), []byte("{not json"), ""); err != nil {
		t.Fatal(err)
	}
	v := &userauth.Visits{Store: s}
	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0); err != nil {
		t.Fatalf("visit over a corrupt file: %v", err)
	}
	if seen, err := v.Seen(ctx, owner); err != nil || seen[dev].Name != domain.PhoneIPhone {
		t.Fatalf("after heal: %+v, %v", seen, err)
	}
}

// Входы не прочитались — ошибка, а не «никто не входил»: окно должно знать,
// что ждать входа бесполезно (ревью #652).
func TestVisitsSeenReportsReadFailure(t *testing.T) {
	s, owner, _, _ := visitsFixture(t)
	s.failGet = errors.New("bucket down")
	if _, err := (&userauth.Visits{Store: s}).Seen(context.Background(), owner); err == nil {
		t.Fatal("unreadable visits reported as none")
	}
}

// Имя, данное в окне, переживает следующие входы телефона и другой процесс
// сервера; вход после переименования время пишет, имя не трогает (#622).
func TestVisitsRename(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, quiet := visitsFixture(t)
	other := id(t, "other")
	if _, err := userauth.Add(ctx, s, other); err != nil {
		t.Fatal(err)
	}
	theirs, _, _ := userauth.AddDevice(ctx, s, other)
	v := &userauth.Visits{Store: s}
	_ = v.Visit(ctx, owner, dev, "Samsung SM-S918B", t0)
	if err := v.Rename(ctx, owner, dev, "Телефон Анастасии"); err != nil {
		t.Fatal(err)
	}
	_ = v.Visit(ctx, owner, dev, "Pixel 8", t0.Add(time.Minute)) // точнее прежнего — но имя дал человек
	_ = (&userauth.Visits{Store: s}).Visit(ctx, owner, dev, "Samsung SM-S918B", t0.Add(userauth.VisitEvery+time.Minute))
	// Переименованный до первого входа: имя есть, входа нет.
	if err := v.Rename(ctx, owner, quiet, "iPhone 17 Pro"); err != nil {
		t.Fatal(err)
	}
	seen, err := v.Seen(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got := seen[dev]; got.Name != "Телефон Анастасии" || !got.At.Equal(t0.Add(userauth.VisitEvery+time.Minute)) {
		t.Errorf("renamed phone after visits: %+v", got)
	}
	if got := seen[quiet]; got.Name != "iPhone 17 Pro" || !got.At.IsZero() {
		t.Errorf("renamed before the first visit: %+v", got)
	}
	if err := v.Rename(ctx, owner, theirs, "Чужой"); !errors.Is(err, domain.ErrNoSuchUser) {
		t.Fatalf("rename of another person's phone: %v", err)
	}
}

// Телефон бросил свой запрос (закрыл приложение, пропала сеть) — это не сбой
// бакета: пауза для всех телефонов процесса не нужна (ревью #652, 3-е).
func TestVisitsNoPauseOnCancel(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, quiet := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	s.failFirst = context.Canceled
	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	if err := v.Visit(ctx, owner, quiet, domain.PhoneAndroid, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if seen, _ := v.Seen(ctx, owner); seen[quiet].At.IsZero() {
		t.Fatal("another phone's visit dropped after a canceled request")
	}
}

// Неудачная запись не откатывает кэш поверх удачной соседней: опрос с моделью,
// записанный, пока первый ждал, остается в кэше (ревью #652, 4-е).
func TestVisitsFailedWriteKeepsNewerCache(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	s.holdFirst, s.failFirst = make(chan struct{}), context.Canceled
	first := make(chan error)
	go func() { first <- v.Visit(ctx, owner, dev, domain.PhoneAndroid, t0) }()
	for s.writes() == 0 {
		time.Sleep(time.Millisecond)
	}
	if err := v.Visit(ctx, owner, dev, "Samsung SM-S918B", t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	close(s.holdFirst)
	if err := <-first; err == nil {
		t.Fatal("first write did not fail")
	}
	_ = v.Visit(ctx, owner, dev, "Samsung SM-S918B", t0.Add(10*time.Second))
	if n := s.writes(); n != 2 {
		t.Fatalf("visits written %d times, want 2: the failed write rolled the cache back", n)
	}
}

// Телефон, чередующий две модели в Sec-CH-UA-Model, не пишет файл на каждом
// опросе: сразу пишется только первое имя с моделью (ревью безопасности #652).
func TestVisitsAlternatingModelsDoNotBypassTheLimit(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	v := &userauth.Visits{Store: s}
	for i := range 20 {
		_ = v.Visit(ctx, owner, dev, []string{"SM-A", "SM-B"}[i%2], t0.Add(time.Duration(i)*time.Second))
	}
	if n := s.writes(); n != 1 {
		t.Fatalf("visits written %d times for 20 polls, want 1", n)
	}
}

// Сбой записи одного человека не останавливает входы других: пауза — своя у
// каждого (ревью безопасности #652).
func TestVisitsPauseIsPerPerson(t *testing.T) {
	ctx := context.Background()
	s, owner, dev, _ := visitsFixture(t)
	other := id(t, "other")
	if _, err := userauth.Add(ctx, s, other); err != nil {
		t.Fatal(err)
	}
	theirs, _, _ := userauth.AddDevice(ctx, s, other)
	v := &userauth.Visits{Store: s}
	s.failFirst = errors.New("bucket down")
	if err := v.Visit(ctx, owner, dev, domain.PhoneIPhone, t0); err == nil {
		t.Fatal("failed write reported no error")
	}
	if err := v.Visit(ctx, other, theirs, domain.PhoneAndroid, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if seen, _ := v.Seen(ctx, other); seen[theirs].At.IsZero() {
		t.Fatal("another person's visit dropped by the pause")
	}
}
