package appdb

import (
	"strings"

	"github.com/google/uuid"
)

// ValidUUID reports whether s is a RFC 4122 UUID.
// Empty strings and other non-UUID actor or cluster locators must never be
// bound to a PostgreSQL uuid column.
func ValidUUID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}
