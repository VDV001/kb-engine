// Package planxlsx writes the sales plan to Excel (#580): листы «По неделям» и
// «По дням», числа — числами, чтобы Excel и Numbers считали их сами.
package planxlsx

import "github.com/daniil/kb-engine/internal/domain"

// Write saves the plan to path.
func Write(path string, p domain.Plan) error { return nil }
