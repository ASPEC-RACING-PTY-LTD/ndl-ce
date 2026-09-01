package appdb

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the Phase 1 control-plane state.
type Store interface {
	GetCluster(ctx context.Context) (*Cluster, error)
	CreateCluster(ctx context.Context, c Cluster) error
	CompleteSetup(ctx context.Context, clusterID string) error

	GetSetup(ctx context.Context) (*SetupToken, error)
	PutSetup(ctx context.Context, clusterID, tokenHash string) error
	ConsumeSetup(ctx context.Context, clusterID string) error

	CreateUser(ctx context.Context, u User) error
	GetUserByName(ctx context.Context, clusterID, username string) (*User, error)
	GetUser(ctx context.Context, id string) (*User, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	CountAdmins(ctx context.Context, clusterID string) (int, error)

	EnsureRoles(ctx context.Context, clusterID string, roles map[string][]string) error
	BindRole(ctx context.Context, clusterID, userID, roleName string) error
	UserRoles(ctx context.Context, userID string) ([]string, error)

	CreateSession(ctx context.Context, s Session) error
	GetSessionByHash(ctx context.Context, hash string) (*Session, error)
	RevokeSession(ctx context.Context, id string) error
	RevokeUserSessions(ctx context.Context, userID string) error

	CreateToken(ctx context.Context, t APIToken) error
	GetTokenByHash(ctx context.Context, hash string) (*APIToken, error)
	RevokeToken(ctx context.Context, id, userID string) error

	UpsertNode(ctx context.Context, n Node) error
	GetNode(ctx context.Context, clusterID string) (*Node, error)

	InsertAudit(ctx context.Context, e AuditEvent) error
}

// Cluster is the appliance cluster of one.
type Cluster struct {
	ID               string
	Name             string
	SetupCompletedAt *time.Time
}

// SetupToken is the hashed one-time claim secret.
type SetupToken struct {
	ClusterID  string
	TokenHash  string
	ConsumedAt *time.Time
}

// User is a local account.
type User struct {
	ID           string
	ClusterID    string
	Username     string
	PasswordHash string
}

// Session is a cookie session.
type Session struct {
	ID        string
	ClusterID string
	UserID    string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// APIToken is a hashed bearer token.
type APIToken struct {
	ID        string
	ClusterID string
	UserID    string
	Name      string
	TokenHash string
	Prefix    string
	RevokedAt *time.Time
}

// Node is the local enrolled node.
type Node struct {
	ID           string
	ClusterID    string
	Name         string
	HostPlatform json.RawMessage
}

// AuditEvent is a security audit row.
type AuditEvent struct {
	ID          string
	ClusterID   string
	ActorUserID string
	Action      string
	Result      string
	RemoteAddr  string
	Detail      json.RawMessage
	CreatedAt   time.Time
}
