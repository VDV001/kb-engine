package domain

import "fmt"

// validateEnum reports whether raw is a member of set. On miss it returns
// invalidErr wrapped with the offending value, so callers stay errors.Is-able.
//
// It centralizes the validation+error-format shared by the string-enum value
// objects (Lifecycle, Verdict, ReadState, PublishStage). Each VO keeps its own
// distinct type so the compiler prevents mixing them.
func validateEnum(raw string, set map[string]struct{}, invalidErr error) error {
	if _, ok := set[raw]; !ok {
		return fmt.Errorf("%w: %q", invalidErr, raw)
	}
	return nil
}
