package cdpbrowser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeCDP is the browser side of the pipe: читает команды (JSON, конец — NUL) и
// отвечает как браузер, у которого при запуске уже есть страницы.
type fakeCDP struct {
	mu    sync.Mutex
	calls []string // «метод» или «метод:целевая страница»
	pages []string // страницы, которые браузер показывает в getTargets

	failCreate bool // браузер отказывает в создании страницы
}

func startFakeCDP(t *testing.T, pages ...string) (*Chrome, *fakeCDP) {
	t.Helper()
	cmdR, cmdW := io.Pipe()
	replyR, replyW := io.Pipe()
	f := &fakeCDP{pages: pages}
	c := newChrome(nil, cmdW)
	go c.readLoop(replyR)
	go func() {
		in := bufio.NewReader(cmdR)
		for {
			raw, err := in.ReadBytes(0)
			if err != nil {
				return
			}
			var m struct {
				ID        int64           `json:"id"`
				Method    string          `json:"method"`
				SessionID string          `json:"sessionId"`
				Params    json.RawMessage `json:"params"`
			}
			if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
				continue
			}
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			f.mu.Lock()
			call := m.Method
			if p.TargetID != "" {
				call += ":" + p.TargetID
			}
			f.calls = append(f.calls, call)
			var res string
			switch m.Method {
			case "Target.getTargets":
				infos := ""
				for i, id := range f.pages {
					if i > 0 {
						infos += ","
					}
					infos += fmt.Sprintf(`{"targetId":%q,"type":"page"}`, id)
				}
				res = `{"targetInfos":[` + infos + `]}`
			case "Target.createTarget":
				res = `{"targetId":"ours"}`
			case "Target.attachToTarget":
				res = fmt.Sprintf(`{"sessionId":"S-%s"}`, p.TargetID)
			case "Page.getFrameTree":
				res = `{"frameTree":{"frame":{"id":"F","url":"about:blank"}}}`
			default:
				res = `{}`
			}
			failed := f.failCreate && m.Method == "Target.createTarget"
			f.mu.Unlock()
			reply := map[string]any{"id": m.ID, "sessionId": m.SessionID, "result": json.RawMessage(res)}
			if failed {
				reply = map[string]any{"id": m.ID, "sessionId": m.SessionID, "error": map[string]any{"code": -32000, "message": "нет окон"}}
			}
			out, _ := json.Marshal(reply)
			if _, err := replyW.Write(append(out, 0)); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = cmdW.Close(); _ = replyW.Close() })
	return c, f
}

func (f *fakeCDP) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Brave при запуске держит две пустые страницы: одна из них — та, что он показывает,
// другая скрыта и кадров не рисует. Раньше сервер брал первую попавшуюся и молча
// получал белый экран. Теперь он делает свою страницу и закрывает чужие.
func TestAttachUsesOwnPageAndClosesOthers(t *testing.T) {
	c, f := startFakeCDP(t, "hidden-1", "shown-2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err != nil {
		t.Fatal(err)
	}
	if got := c.session(); got != "S-ours" {
		t.Fatalf("сессия = %q, want S-ours: сервер должен работать со своей страницей", got)
	}
	calls := f.seen()
	for _, want := range []string{"Target.createTarget", "Target.attachToTarget:ours", "Target.closeTarget:hidden-1", "Target.closeTarget:shown-2"} {
		if !slices.Contains(calls, want) {
			t.Errorf("не было вызова %q; вызовы: %v", want, calls)
		}
	}
	for _, bad := range []string{"Target.attachToTarget:hidden-1", "Target.attachToTarget:shown-2"} {
		if slices.Contains(calls, bad) {
			t.Errorf("сервер подключился к чужой странице: %q", bad)
		}
	}
	c.mu.Lock()
	pages := slices.Clone(c.pages)
	c.mu.Unlock()
	if len(pages) != 1 || pages[0].target != "ours" {
		t.Errorf("открытые страницы = %+v, want только ours", pages)
	}
}

// Чужие страницы закрываются только когда своя уже есть: если создать ее не удалось,
// браузер остался бы совсем без окон.
func TestAttachClosesNothingWhenOwnPageFails(t *testing.T) {
	c, f := startFakeCDP(t, "hidden-1", "shown-2")
	f.failCreate = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.attach(ctx, Options{Width: 1280, Height: 800}); err == nil {
		t.Fatal("attach без своей страницы должен вернуть ошибку")
	}
	for _, call := range f.seen() {
		if call == "Target.closeTarget:hidden-1" || call == "Target.closeTarget:shown-2" {
			t.Fatalf("закрыта чужая страница %q, хотя своей нет", call)
		}
	}
}
