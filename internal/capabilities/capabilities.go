package capabilities

// ID is a platform capability name. The catalog is deny-by-default.
// Phase 0 has no enabled capabilities and no database table.
type ID string

// DefaultEnabled is empty in Community Edition until later phases
// opt in to optional modules. This is not a root plugin loader.
func DefaultEnabled() []ID {
	return nil
}
