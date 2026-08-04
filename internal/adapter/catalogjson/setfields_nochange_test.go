package catalogjson_test

import (
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/catalogjson"
)

// «1 entry(ies) updated» печаталось и тогда, когда запись не менялась: счёт вёлся
// по записям, к которым правку применили, а не по тем, что от неё изменились.
// Это «выполнено» без содержания — после такого ответа никто не приходит
// проверять, а правка на деле ничего не сделала.
func TestSetFields_countsOnlyRealChanges(t *testing.T) {
	path := writeFixture(t)

	// Запись 2 уже в состоянии active.
	n, err := catalogjson.SetFields(path, []int{2}, catalogjson.Changes{Lifecycle: "active"})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if n != 0 {
		t.Errorf("updated = %d, ожидался 0: запись уже в этом состоянии", n)
	}
}

// Тег, который уже стоит, добавить нельзя дважды — и отчитываться о нём как об
// изменении тоже нельзя.
func TestSetFields_addingAnExistingTagChangesNothing(t *testing.T) {
	path := writeFixture(t)

	n, err := catalogjson.SetFields(path, []int{1}, catalogjson.Changes{AddTags: []string{"go"}})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if n != 0 {
		t.Errorf("updated = %d, ожидался 0: тег «go» уже стоит", n)
	}
}

// Настоящая правка по-прежнему считается.
func TestSetFields_countsAGenuineChange(t *testing.T) {
	path := writeFixture(t)

	n, err := catalogjson.SetFields(path, []int{2}, catalogjson.Changes{Lifecycle: "outdated"})
	if err != nil {
		t.Fatalf("SetFields: %v", err)
	}
	if n != 1 {
		t.Errorf("updated = %d, ожидался 1", n)
	}
}
