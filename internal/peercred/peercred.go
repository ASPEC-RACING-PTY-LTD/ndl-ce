package peercred

// Creds are SO_PEERCRED fields.
type Creds struct {
	UID uint32
	GID uint32
	PID int32
}
