// Package changelog parses a Keep-a-Changelog markdown file into a structured
// document for the dashboard. It is a pure inbound adapter: markdown in,
// structs out. The on-disk JSON shape is produced by Document.MarshalJSON.
package changelog

import (
	"regexp"
	"strings"
)

// Section is a named group of changelog bullets (e.g. "Added", "Changed"),
// kept in document order.
type Section struct {
	Name  string
	Items []string
}

// Release is one version block. Date is "" for the Unreleased pseudo-release.
type Release struct {
	Version  string
	Date     string
	Tagline  string
	Sections []Section
}

// Document is the parsed changelog: the releases newest-first plus convenience
// pointers to the latest released version.
type Document struct {
	CurrentVersion string
	CurrentDate    string
	CurrentTagline string
	Releases       []Release
}

var (
	reVersion    = regexp.MustCompile(`^##\s*\[([^\]]+)\]\s*(?:—|-)\s*(\d{4}-\d{2}-\d{2})\s*$`)
	reUnreleased = regexp.MustCompile(`(?i)^##\s*\[Unreleased\]\s*$`)
	reSection    = regexp.MustCompile(`^###\s+(.+?)\s*$`)
	reTagline    = regexp.MustCompile(`^>\s*(.+?)\s*$`)
	reBullet     = regexp.MustCompile(`^[-*+]\s+(.+?)\s*$`)
)

// Parse turns changelog markdown into a Document. Releases appear in file order
// (newest first by convention). An empty Unreleased block is dropped.
func Parse(text string) Document {
	var p parser
	for raw := range strings.SplitSeq(text, "\n") {
		p.feed(strings.TrimRight(raw, " \t\r"))
	}
	return assemble(p.releases)
}

// parser holds the streaming state while consuming changelog lines.
type parser struct {
	releases []*Release
	current  *Release
	section  *Section
}

// feed advances the parser by one line.
func (p *parser) feed(line string) {
	if reUnreleased.MatchString(line) {
		p.startRelease(&Release{Version: "Unreleased"})
		return
	}
	if m := reVersion.FindStringSubmatch(line); m != nil {
		p.startRelease(&Release{Version: m[1], Date: m[2]})
		return
	}
	if p.current == nil {
		return
	}
	if m := reTagline.FindStringSubmatch(line); m != nil && p.current.Tagline == "" {
		p.current.Tagline = m[1]
		return
	}
	if m := reSection.FindStringSubmatch(line); m != nil {
		p.startSection(m[1])
		return
	}
	p.feedItem(line)
}

func (p *parser) startRelease(r *Release) {
	p.current = r
	p.section = nil
	p.releases = append(p.releases, r)
}

// startSection points the parser at the named section, reusing an existing one
// of the same name (so repeated "### Added" blocks merge into one) rather than
// emitting a duplicate key in the JSON sections object.
func (p *parser) startSection(name string) {
	for i := range p.current.Sections {
		if p.current.Sections[i].Name == name {
			p.section = &p.current.Sections[i]
			return
		}
	}
	p.current.Sections = append(p.current.Sections, Section{Name: name})
	p.section = &p.current.Sections[len(p.current.Sections)-1]
}

// feedItem handles bullets and indented continuation lines within a section.
func (p *parser) feedItem(line string) {
	if p.section == nil {
		return
	}
	if m := reBullet.FindStringSubmatch(line); m != nil {
		p.section.Items = append(p.section.Items, m[1])
		return
	}
	if strings.HasPrefix(line, "  ") && len(p.section.Items) > 0 {
		if trimmed := strings.Trim(line, "-*+ \t"); trimmed != "" {
			p.section.Items[len(p.section.Items)-1] += " " + trimmed
		}
	}
}

// assemble drops empty Unreleased blocks and derives the current-version
// pointers from the newest released entry.
func assemble(releases []*Release) Document {
	out := make([]Release, 0, len(releases))
	for _, r := range releases {
		if r.Version == "Unreleased" && !hasContent(*r) {
			continue
		}
		out = append(out, *r)
	}

	doc := Document{CurrentVersion: "0.0.0", Releases: out}
	for _, r := range out {
		if r.Version != "Unreleased" {
			doc.CurrentVersion = r.Version
			doc.CurrentDate = r.Date
			doc.CurrentTagline = r.Tagline
			break
		}
	}
	return doc
}

func hasContent(r Release) bool {
	for _, s := range r.Sections {
		if len(s.Items) > 0 {
			return true
		}
	}
	return false
}
