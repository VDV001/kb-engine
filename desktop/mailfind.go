package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/usecase/chat"
	"github.com/daniil/kb-engine/internal/usecase/mailrelay"
)

const (
	phoneHits    = 20
	phoneSnippet = 160
)

type phoneFinder struct{}

func newPhoneFinder(func(context.Context, string, int) ([]chat.Doc, error), func(context.Context, string) (chat.Doc, error)) *phoneFinder {
	return &phoneFinder{}
}

func (f *phoneFinder) Search(context.Context, string) ([]mailrelay.Hit, error) { return nil, nil }
func (f *phoneFinder) Open(context.Context, string) (mailrelay.Hit, error) {
	return mailrelay.Hit{}, nil
}

func answerSources(chatRecord) []mailrelay.Source { return nil }
