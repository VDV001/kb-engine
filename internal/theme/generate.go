package theme

// The palette has one source, design/tokens.json, and two consumers that must
// never disagree. Both outputs are committed so a build needs no generator, and
// a test re-renders them to catch a source edited without regenerating.
//
//go:generate go run ./gentokens -tokens ../../design/tokens.json -go tokens.go -css ../../frontend/src/tokens.css
