package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/daniil/kb-engine/internal/adapter/embedhttp"
	"github.com/daniil/kb-engine/internal/adapter/mcpserver"
	"github.com/daniil/kb-engine/internal/adapter/memorydir"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/memory"
)

// memoryAgent is the agent's door to the person's memory: найти и предложить, и
// больше ничего (ADR-0008, ревью #521). Одобрить предложение агент не может: у
// сервера нет такого инструмента, а решает человек в окне по Touch ID.
type memoryAgent struct{ dir string }

func (a memoryAgent) service() (*memory.Service, error) {
	st := memorydir.New(a.dir)
	lim, err := st.Limits()
	if err != nil {
		return nil, err
	}
	return memory.New(st, lim).WithEmbedder(embedhttp.New(memorydir.EmbedURL, memorydir.EmbedModel), memorydir.EmbedModel), nil
}

// exists: памяти нет — предложение некуда класть, и папку по просьбе агента не заводят.
func (a memoryAgent) exists() error {
	_, err := os.Stat(filepath.Join(a.dir, "HOT.md"))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("память еще не заведена: kbengine memory init")
	}
	return err
}

func (a memoryAgent) Find(query string, limit int) ([]mcpserver.MemoryHit, string, error) {
	if err := a.exists(); err != nil {
		return nil, "", err
	}
	svc, err := a.service()
	if err != nil {
		return nil, "", err
	}
	res, err := svc.Find(query, memory.FindOptions{Limit: limit})
	if err != nil {
		return nil, "", err
	}
	out := make([]mcpserver.MemoryHit, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, mcpserver.MemoryHit{Name: h.Note.Name, Topic: h.Note.Topic, Description: h.Note.Description, Score: h.Score})
	}
	return out, res.Via, nil
}

// Trust: режим по здоровью памяти. Здоровье читает те же файлы, что окно и терминал.
func (a memoryAgent) Trust() (mcpserver.MemoryTrust, error) {
	if err := a.exists(); err != nil {
		return mcpserver.MemoryTrust{}, err
	}
	svc, err := a.service()
	if err != nil {
		return mcpserver.MemoryTrust{}, err
	}
	h, err := svc.Health()
	if err != nil {
		return mcpserver.MemoryTrust{}, err
	}
	t := mcpserver.MemoryTrust{Mode: string(h.Mode), QueueFull: h.Pending >= domain.QueueHardCap}
	for _, r := range h.Rules {
		if r.Stale {
			t.StaleRules = append(t.StaleRules, r.Text)
		}
	}
	return t, nil
}

func (a memoryAgent) Propose(name, topic, description, body string) error {
	if err := a.exists(); err != nil {
		return err
	}
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.Propose(name, topic, description, body)
}
