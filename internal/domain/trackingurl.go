package domain

import (
	"net/url"
	"strings"
)

// trackingParams are query parameters that describe how someone arrived at a
// page rather than which page it is. Kept as an explicit list: stripping
// anything that merely looks like tracking would eventually delete a real
// parameter, and a url the catalog can no longer resolve is worse than a tidy
// one.
var trackingParams = map[string]struct{}{
	"fbclid": {}, "gclid": {}, "yclid": {}, "igshid": {},
	"mc_cid": {}, "mc_eid": {}, "_openstat": {}, "yadclid": {},
}

// utmPrefix covers the whole utm_ family in one rule — utm_campaign,
// utm_source, utm_medium, utm_term, utm_content and whatever else a digest
// appends.
const utmPrefix = "utm_"

// StripTrackingParams removes campaign bookkeeping from a url and returns the
// address of the thing itself.
//
// Input that does not parse is returned unchanged: this runs over a catalog
// that outlived several tools, and refusing to touch what it does not
// understand is the only safe answer.
func StripTrackingParams(raw string) string {
	if !strings.Contains(raw, "?") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	// Rebuilt by hand rather than via Values.Encode(): that sorts keys, and a
	// reshuffled query would show up as a change on entries where nothing but
	// the tracking tail was removed.
	kept := make([]string, 0, 4)
	for _, pair := range strings.Split(u.RawQuery, "&") {
		if pair == "" {
			continue
		}
		name, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(name, utmPrefix) {
			continue
		}
		if _, ok := trackingParams[name]; ok {
			continue
		}
		kept = append(kept, pair)
	}

	u.RawQuery = strings.Join(kept, "&")
	return u.String()
}
