package main

import (
	"context"

	"github.com/daniil/kb-engine/internal/usecase/chat"
)

// maxProposalBytes — предел текста новой итерации из чата.
const maxProposalBytes = 1 << 20

// pendingIteration is a new iteration that the chat offered and the person has not answered yet.
type pendingIteration struct {
	ID, ChatID, Family, Folder, File, Text, Description, Note string
}

// iterationView is a proposal as the page shows it.
type iterationView struct {
	ID          string `json:"id"`
	ChatID      string `json:"chatId"`
	Family      string `json:"family"`
	Folder      string `json:"folder"`
	File        string `json:"file"`
	Description string `json:"description"`
	Note        string `json:"note"`
	Size        int    `json:"size"`
}

func (d *chatDesk) proposeVersionFor(chatID string) func(ctx context.Context, p chat.VersionProposal) error {
	return func(context.Context, chat.VersionProposal) error { return nil }
}

func (d *chatDesk) versionProposals() []iterationView { return nil }

func (d *chatDesk) saveVersionProposal(ctx context.Context, id, description, note string) (string, error) {
	return "", nil
}

func (d *chatDesk) dropVersionProposal(id string) {}
