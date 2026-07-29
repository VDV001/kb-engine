package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The families are named in design/tokens.json and the files are declared in
// fonts.css. Nothing forces those two to agree, and a disagreement is silent:
// the page keeps rendering, in a fallback face, and only a person who knows
// what the font should look like notices.
//
// A generator for the @font-face blocks would also close this, and would be
// more code than the drift is worth — a static list of eight declarations does
// not need one. Checking is enough.
func TestFontFacesCoverEveryFamily(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	d, err := load(filepath.Join(root, "design", "tokens.json"))
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	cssPath := filepath.Join(root, "frontend", "src", "fonts.css")
	raw, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatalf("read %s: %v", cssPath, err)
	}
	css := string(raw)

	for role, family := range d.Fonts {
		if !strings.Contains(css, fmt.Sprintf("font-family: '%s';", family)) {
			t.Errorf("font role %q names %q, which has no @font-face in fonts.css", role, family)
		}
	}

	// Every declared file has to exist, or the browser silently falls back and
	// the binary ships a stylesheet pointing at nothing.
	refs := regexp.MustCompile(`url\(([^)]+)\)`).FindAllStringSubmatch(css, -1)
	if len(refs) == 0 {
		t.Fatal("fonts.css declares no font files")
	}
	for _, m := range refs {
		rel := strings.Trim(m[1], `'"`)
		if _, err := os.Stat(filepath.Join(root, "frontend", "src", rel)); err != nil {
			t.Errorf("fonts.css points at %s, which is not there: %v", rel, err)
		}
	}
}

// OFL allows redistribution and requires the licence to travel with the fonts.
// Embedding them in a binary that ships under AGPL is exactly redistribution,
// so a missing licence file is a compliance defect, not tidiness.
func TestEveryFontFamilyShipsItsLicence(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "frontend", "src", "assets", "fonts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	families := map[string]bool{}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".woff2"); ok {
			family, _, _ := strings.Cut(name, "-")
			families[family] = false
		}
	}
	if len(families) == 0 {
		t.Fatal("no font files found")
	}
	for family := range families {
		path := filepath.Join(dir, family+"-OFL.txt")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s ships without its licence (%s): %v", family, filepath.Base(path), err)
		}
	}
}
