//go:build netprobe

// Отрицательный контроль к прогону без сети: этот тест сеть ТРОГАЕТ, поэтому
// без неё обязан упасть. Без него «весь набор зелёный под --network none»
// доказывает лишь то, что тесты вообще запустились — изоляция могла и не
// сработать.
//
// Тег держит его вне обычных прогонов: он единственный в репозитории, кому
// сеть нужна, и в наборе, который гоняют каждый день, ему делать нечего.
package embedhttp

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNetworkIsReachable(t *testing.T) {
	var r net.Resolver
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.LookupHost(ctx, "proxy.golang.org"); err != nil {
		t.Fatalf("сеть недоступна: %v", err)
	}
}
