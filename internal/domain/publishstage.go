package domain

import "errors"

// ErrInvalidPublishStage is returned when a publish-stage value is not canonical.
var ErrInvalidPublishStage = errors.New("invalid publish stage")

// PublishStage is a value object for the stage of an owner-authored creation
// (e.g. an article). It is immutable; construct it via NewPublishStage.
type PublishStage struct {
	value string
}

var canonicalPublishStages = map[string]struct{}{
	"draft":     {},
	"final":     {},
	"published": {},
}

// NewPublishStage validates raw against the canonical set and returns a
// PublishStage.
func NewPublishStage(raw string) (PublishStage, error) {
	if err := validateEnum(raw, canonicalPublishStages, ErrInvalidPublishStage); err != nil {
		return PublishStage{}, err
	}
	return PublishStage{value: raw}, nil
}

// String returns the canonical publish-stage value.
func (p PublishStage) String() string {
	return p.value
}
