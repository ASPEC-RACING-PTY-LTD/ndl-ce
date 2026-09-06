package license

import (
	"errors"
	"time"
)

type Input struct {
	Now            time.Time
	HasKey         bool
	Key            string
	Cached         *Document
	CachedSigned   bool
	ProbeErr       error
	RuntimePresent bool
	EEBlobsPresent bool
	LastChecked    time.Time
	Unreachable    bool
}

const (
	ReasonAbsent           = "Community Edition. License activation is not required."
	ReasonActive           = "Enterprise entitlement is valid. Workloads are not stopped."
	ReasonActiveNoRuntime  = "Entitlement is valid. Enterprise runtime is not installed. Community Edition continues. Workloads are not stopped."
	ReasonGraceUnreachable = "Licensing API unreachable. Cached entitlement remains in grace. Workloads are not stopped."
	ReasonGraceNotEntitled = "Licensing API did not grant entitlement. Grace applies. Workloads are not stopped."
	ReasonExpired          = "Enterprise entitlement expired. Enterprise capabilities are disabled. Community Edition continues. Workloads are not stopped."
	ReasonUnsignedActive   = "Key accepted. Signed Enterprise artifacts are required to enable Enterprise capabilities. Workloads are not stopped."
)

func Evaluate(in Input) Snapshot {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := Snapshot{
		Edition:          EditionCE,
		Status:           StatusAbsent,
		Reason:           ReasonAbsent,
		HasKey:           in.HasKey,
		KeySuffix:        Last4(in.Key),
		WorkloadsStopped: false,
		EEBlobs:          in.EEBlobsPresent,
		EERuntime:        in.RuntimePresent,
		ContactsAPI:      in.HasKey,
	}
	if !in.LastChecked.IsZero() {
		out.LastChecked = formatTime(in.LastChecked)
	}
	if !in.HasKey && in.Cached == nil {
		return out
	}
	doc := in.Cached
	if doc != nil {
		out.Organization = doc.Organization
		out.InstallationID = doc.InstallationID
		out.SubscriptionID = doc.SubscriptionID
		out.UpdateChannel = doc.UpdateChannel
		out.ExpiresAt = doc.ExpiresAt
		out.GraceUntil = doc.GraceUntil
		out.Signed = in.CachedSigned
	}
	if in.Unreachable || isUnreachable(in.ProbeErr) {
		if doc != nil && in.CachedSigned && withinGrace(doc, now) {
			return entitledSnapshot(out, doc, in.RuntimePresent, StatusGrace, ReasonGraceUnreachable)
		}
		out.Status = StatusUnreachable
		out.Reason = ReasonGraceUnreachable
		out.Degraded = true
		return out
	}
	if isNotEntitled(in.ProbeErr) {
		out.Status = StatusGrace
		out.Reason = ReasonGraceNotEntitled
		out.Degraded = true
		return out
	}
	if doc == nil {
		if in.HasKey {
			out.Status = StatusGrace
			out.Reason = ReasonGraceNotEntitled
			out.Degraded = true
		}
		return out
	}
	if in.CachedSigned {
		if !doc.graceUntil().IsZero() && now.After(doc.graceUntil()) {
			out.Status = StatusExpired
			out.Reason = ReasonExpired
			out.Degraded = true
			out.Capabilities = nil
			out.Edition = EditionCE
			return out
		}
		if !doc.expiresAt().IsZero() && now.After(doc.expiresAt()) {
			return entitledSnapshot(out, doc, in.RuntimePresent, StatusGrace, ReasonGraceUnreachable)
		}
		return entitledSnapshot(out, doc, in.RuntimePresent, StatusActive, ReasonActive)
	}
	if doc.Accepted || doc.Entitled {
		out.Status = StatusActive
		out.Reason = ReasonUnsignedActive
		return out
	}
	out.Status = StatusGrace
	out.Reason = ReasonGraceNotEntitled
	out.Degraded = true
	return out
}

func entitledSnapshot(base Snapshot, doc *Document, runtime bool, status, reason string) Snapshot {
	base.Status = status
	base.Signed = true
	base.Organization = doc.Organization
	base.InstallationID = doc.InstallationID
	base.SubscriptionID = doc.SubscriptionID
	base.UpdateChannel = doc.UpdateChannel
	base.ExpiresAt = doc.ExpiresAt
	base.GraceUntil = doc.GraceUntil
	base.WorkloadsStopped = false
	base.Degraded = status != StatusActive
	if runtime {
		base.Edition = EditionEE
		base.Capabilities = FilterKnown(doc.Capabilities)
		base.Reason = reason
		return base
	}
	base.Edition = EditionCE
	if status == StatusActive {
		base.Reason = ReasonActiveNoRuntime
	} else {
		base.Reason = reason
	}
	return base
}

func withinGrace(doc *Document, now time.Time) bool {
	g := doc.graceUntil()
	if g.IsZero() {
		exp := doc.expiresAt()
		if exp.IsZero() {
			return true
		}
		return !now.After(exp.Add(DefaultGrace))
	}
	return !now.After(g)
}

func isUnreachable(err error) bool {
	return errors.Is(err, ErrUnreachable)
}

func isNotEntitled(err error) bool {
	return errors.Is(err, ErrNotEntitled)
}
