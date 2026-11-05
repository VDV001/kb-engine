package blobs3

import (
	"net/http"
	"testing"
)

// Токен сервисного аккаунта не должен идти через HTTP_PROXY: прокси увидел бы
// его открытым текстом. Проверяется транспорт, а не поведение под t.Setenv:
// net/http читает прокси из окружения один раз на процесс, и тест через
// переменные окружения был зеленым и без защиты (замерено 05.11).
func TestMetadataClientHasNoProxy(t *testing.T) {
	tr, ok := metadataClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("metadata client transport is %T, want an *http.Transport without a proxy", metadataClient.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("metadata client transport has a Proxy func")
	}
}
