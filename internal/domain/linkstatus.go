package domain

import (
	"errors"
	"fmt"
)

// ErrInvalidLinkStatus is returned when a link status value is not canonical.
var ErrInvalidLinkStatus = errors.New("invalid link status")

// Link statuses. They are deliberately four rather than two: a scan that only
// says alive/dead has to guess about the codes that carry no verdict.
const (
	linkAlive       = "alive"
	linkMoved       = "moved"
	linkGone        = "gone"
	linkUndecidable = "undecidable"
)

var canonicalLinkStatuses = map[string]struct{}{
	linkAlive: {}, linkMoved: {}, linkGone: {}, linkUndecidable: {},
}

// LinkStatus is what a drift scan learned about one URL.
type LinkStatus struct {
	value string
}

// NewLinkStatus validates raw against the canonical set.
func NewLinkStatus(raw string) (LinkStatus, error) {
	if err := validateEnum(raw, canonicalLinkStatuses, ErrInvalidLinkStatus); err != nil {
		return LinkStatus{}, err
	}
	return LinkStatus{value: raw}, nil
}

// ClassifyLinkStatus maps an HTTP status code to what it says about the
// article — which for several codes is nothing.
//
// 403 is the case that matters: habr answers it both for a withdrawn article
// and for a bot it declines to serve, and every 403 in the live catalog is
// habr. Recording those as gone would bury articles that are still up;
// recording them as alive would hide the withdrawn ones. Neither is true, so
// the status says so and the report tells the owner a browser is needed.
func ClassifyLinkStatus(code int) (LinkStatus, error) {
	if code < 100 || code > 599 {
		return LinkStatus{}, fmt.Errorf("%w: %d is not an HTTP status code", ErrInvalidLinkStatus, code)
	}
	switch {
	case code >= 200 && code < 300:
		return LinkStatus{value: linkAlive}, nil
	case code >= 300 && code < 400:
		return LinkStatus{value: linkMoved}, nil
	case code == 404 || code == 410:
		return LinkStatus{value: linkGone}, nil
	default:
		return LinkStatus{value: linkUndecidable}, nil
	}
}

// String returns the canonical status value.
func (s LinkStatus) String() string { return s.value }

// IsActionable reports whether the status alone justifies changing the entry.
// Only «gone» does: moved still resolves, and undecidable is the engine saying
// it does not know.
func (s LinkStatus) IsActionable() bool { return s.value == linkGone }
