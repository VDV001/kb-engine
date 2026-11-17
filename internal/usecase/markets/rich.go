package markets

import "errors"

var ErrModelRich = errors.New("stub")

const MaxRichText = 300

type RichInput struct {
	Title, Description string
	Praise, Complaints []string
	Photos             int
}

type RichBlock struct {
	Title, Text string
	Photo       int
}

type RichVariant struct {
	Name   string
	Blocks []RichBlock
}

type RichPhoto struct {
	URL           string
	Width, Height int
}

func RichPrompt(RichInput) string { return "" }

func ParseRichVariants(string, int) ([]RichVariant, error) { return nil, nil }

func RichJSON(RichVariant, []RichPhoto) ([]byte, error) { return nil, nil }
