package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	tokens := flag.String("tokens", "../../design/tokens.json", "path to the token source")
	goOut := flag.String("go", "tokens.go", "where to write the Go palette")
	cssOut := flag.String("css", "../../frontend/src/tokens.css", "where to write the CSS custom properties")
	flag.Parse()

	if err := run(*tokens, *goOut, *cssOut); err != nil {
		fmt.Fprintf(os.Stderr, "gentokens: %v\n", err)
		os.Exit(1)
	}
}

// run renders both outputs before writing either. A half-generated pair is the
// drift this command exists to prevent.
func run(tokens, goOut, cssOut string) error {
	d, err := load(tokens)
	if err != nil {
		return err
	}
	src, err := renderGo(d)
	if err != nil {
		return err
	}
	css, err := renderCSS(d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(goOut, []byte(src), 0o644); err != nil { //nolint:gosec // generated source, not a secret
		return fmt.Errorf("write %s: %w", goOut, err)
	}
	if err := os.WriteFile(cssOut, []byte(css), 0o644); err != nil { //nolint:gosec // generated stylesheet, not a secret
		return fmt.Errorf("write %s: %w", cssOut, err)
	}
	return nil
}
