package license

import (
	"errors"
	"testing"
	"time"
)

func TestEvaluateNeverStopsWorkloads(t *testing.T) {
	signer, _, err := NewEphemeralSigner("ee-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	doc, err := signer.Sign(Document{
		Version: DocumentVersion, Edition: EditionEE, Organization: "Org",
		Capabilities: []string{CapIdentityOIDC, AuditExport},
		IssuedAt:     now.Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
		GraceUntil: now.Add(3 * time.Hour).Format(time.RFC3339), Accepted: true, Entitled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []Input{
		{Now: now},
		{Now: now, HasKey: true, Key: "NDL-EE-AAAA-BBBB", Cached: &doc, CachedSigned: true, RuntimePresent: true, EEBlobsPresent: true},
		{Now: now.Add(90 * time.Minute), HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: true, Unreachable: true},
		{Now: now.Add(4 * time.Hour), HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: true},
		{Now: now, HasKey: true, ProbeErr: ErrNotEntitled},
		{Now: now, HasKey: true, ProbeErr: ErrUnreachable},
		{Now: now, HasKey: true, Cached: &Document{Accepted: true}, CachedSigned: false},
		{Now: now, HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: false},
		{Now: now, HasKey: true, ProbeErr: errors.Join(ErrUnreachable)},
	}
	for i, in := range cases {
		snap := Evaluate(in)
		if snap.WorkloadsStopped {
			t.Fatalf("case %d stopped workloads: %+v", i, snap)
		}
	}
	active := Evaluate(Input{Now: now, HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: true, EEBlobsPresent: true})
	if active.Edition != EditionEE || active.Status != StatusActive || !HasCapability(active.Capabilities, CapIdentityOIDC) {
		t.Fatalf("active %+v", active)
	}
	expired := Evaluate(Input{Now: now.Add(4 * time.Hour), HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: true})
	if expired.Edition != EditionCE || expired.Status != StatusExpired || expired.WorkloadsStopped {
		t.Fatalf("expired %+v", expired)
	}
	noRuntime := Evaluate(Input{Now: now, HasKey: true, Cached: &doc, CachedSigned: true, RuntimePresent: false})
	if noRuntime.Edition != EditionCE || noRuntime.Status != StatusActive {
		t.Fatalf("no runtime %+v", noRuntime)
	}
	unsigned := Evaluate(Input{Now: now, HasKey: true, Cached: &Document{Accepted: true}, CachedSigned: false})
	if unsigned.Edition != EditionCE || unsigned.Status != StatusActive {
		t.Fatalf("unsigned %+v", unsigned)
	}
}
