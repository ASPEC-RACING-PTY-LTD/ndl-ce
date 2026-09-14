package httpapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

var auditSecretKeys = []string{
	"password", "password_hash", "token", "secret", "mfa", "recovery",
	"credential", "private_key", "encryption", "api_key", "authorization",
}

func auditActorKind(u *appdb.User, actorID string) string {
	if strings.TrimSpace(actorID) == "" {
		return "system"
	}
	if u == nil {
		return "deleted"
	}
	if u.Kind == appdb.UserKindService {
		return "service"
	}
	return "user"
}

func auditActorName(u *appdb.User, actorID, joinedName string) string {
	if strings.TrimSpace(joinedName) != "" {
		return joinedName
	}
	if u != nil && u.Username != "" {
		return u.Username
	}
	if strings.TrimSpace(actorID) == "" {
		return "System"
	}
	return "Deleted account"
}

func sanitizeAuditDetail(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	out := make(map[string]any, len(obj))
	for key, value := range obj {
		lk := strings.ToLower(key)
		blocked := false
		for _, secret := range auditSecretKeys {
			if strings.Contains(lk, secret) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func auditResourceFromDetail(detail map[string]any) (kind, name, id string) {
	if detail == nil {
		return "", "", ""
	}
	if v, ok := detail["detail"].(string); ok {
		v = strings.TrimSpace(v)
		if appdb.ValidUUID(v) {
			id = v
		} else if v != "" && !looksSecret(v) {
			name = v
		}
	}
	if v, ok := detail["name"].(string); ok && strings.TrimSpace(v) != "" {
		name = strings.TrimSpace(v)
	}
	if v, ok := detail["workload_id"].(string); ok && appdb.ValidUUID(v) {
		id = v
		kind = "workload"
	}
	return kind, name, id
}

func looksSecret(v string) bool {
	lv := strings.ToLower(v)
	return strings.Contains(lv, "ndl_") || strings.Contains(lv, "secret") || strings.Contains(lv, "password")
}

func (s *Server) resolveAuditActor(ctx context.Context, actorID string) (*appdb.User, string) {
	if !appdb.ValidUUID(actorID) {
		return nil, ""
	}
	u, err := s.Store.GetUser(ctx, actorID)
	if err != nil || u == nil {
		return nil, ""
	}
	return u, u.Username
}
