package httpapi

import (
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

// Две строки, записанные в одну секунду, но в разные миллисекунды, обязаны
// отличаться и на проводе. RFC3339 режет доли секунды, поэтому витрина видела
// одинаковые метки, считала момент равным и падала обратно в порядок файла —
// тогда как терминал сравнивал полные миллисекунды и давал обратный порядок.
// На живой книге так расходились 10 строк в 4 корзинах.
func TestRecordedAt_KeepsMillisecondsOnTheWire(t *testing.T) {
	base := time.Date(2026, 8, 2, 12, 30, 45, 0, time.UTC)
	entropy := ulid.Monotonic(rand.New(rand.NewSource(1)), 0) //nolint:gosec // детерминизм теста важнее криптостойкости

	earlier := ulid.MustNew(ulid.Timestamp(base.Add(120*time.Millisecond)), entropy).String()
	later := ulid.MustNew(ulid.Timestamp(base.Add(870*time.Millisecond)), entropy).String()

	got1 := recordedAtOrEmpty(earlier)
	got2 := recordedAtOrEmpty(later)

	if got1 == got2 {
		t.Fatalf("две строки одной секунды получили одинаковый момент на проводе: %q\n"+
			"витрина не сможет их упорядочить и вернётся к порядку файла", got1)
	}
	if !(got1 < got2) {
		t.Fatalf("строковое сравнение меток не сохраняет порядок: %q должно быть < %q", got1, got2)
	}
}
