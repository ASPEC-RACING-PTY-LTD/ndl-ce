package httpapi

import (
	"log"
	"net/http"
	"strings"

	"github.com/no-dal/ndl-ce/internal/rbac"
)

// canonicalRole maps a role id from before roles were renamed to match the
// UI (migration 0076: owner/admin -> superadmin, operator -> admin, viewer
// -> user) to its current id. The database only holds current ids; this
// catches API clients and scripts that still send old ones, and logs each
// so they can be updated. Remove together with rbac.LegacyRole.
func canonicalRole(r *http.Request, name string) string {
	name = strings.TrimSpace(name)
	if cur, ok := rbac.LegacyRole(name); ok {
		log.Printf("api: %s %s sent the old role id %q; it is now %q", r.Method, r.URL.Path, name, cur)
		return cur
	}
	return name
}

func canonicalRoles(r *http.Request, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, canonicalRole(r, n))
	}
	return out
}
