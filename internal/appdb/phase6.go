package appdb

import "time"

const (
	IOTargetHost            = "host"
	IOTargetSystemContainer = "system-container"
	IOKindTerminal          = "terminal"
	IOKindConsole           = "console"
	IOStatePending          = "pending"
	IOStateConnected        = "connected"
	IOStateEnded            = "ended"
	IOStateExpired          = "expired"
)

// IOSession is a ticketed Terminal session. The plaintext ticket is never stored.
type IOSession struct {
	ID          string
	ClusterID   string
	UserID      string
	TargetKind  string
	TargetID    string
	Kind        string
	CWD         string
	TicketHash  string
	State       string
	Reason      string
	ExpiresAt   time.Time
	ConnectedAt *time.Time
	EndedAt     *time.Time
	CreatedAt   time.Time
}
