package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Converter runs a validated qemu-img argv. Tests replace this.
type Converter func(ctx context.Context, argv []string) error

// Engine runs copy-first import and export. It has no source-delete path.
type Engine struct {
	StagingRoot string
	Convert     Converter
	Formats     map[string]bool
	Now         func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

func (e *Engine) stagingRoot() string {
	if e.StagingRoot != "" {
		return e.StagingRoot
	}
	return StagingRoot
}

func BuildPlan(id, adapter string, discovered []DiscoveredWorkload, selected []string, modes map[string]string, manifests map[string]Manifest, mapping Mapping, overrides map[string]Mapping, qemuFormats map[string]bool, startAfter bool, liveAck map[string]bool) (Plan, error) {
	p := Plan{ID: id, Direction: "import", Adapter: adapter, Mapping: mapping, StartAfter: startAfter}
	sel := map[string]struct{}{}
	for _, id := range selected {
		sel[id] = struct{}{}
	}
	for _, w := range discovered {
		if _, ok := sel[w.SourceID]; !ok {
			continue
		}
		mode := modes[w.SourceID]
		if mode == "" {
			return Plan{}, fmt.Errorf("migration mode must be selected for %s", w.Name)
		}
		m := manifests[w.SourceID]
		if m.Kind == "" {
			m.Kind = w.Kind
			m.Identity = Identity{Name: w.Name, SourceID: w.SourceID}
		}
		var ov *Mapping
		if o, ok := overrides[w.SourceID]; ok {
			ov = &o
			item := o
			_ = item
		}
		mapped := ApplyMapping(m, mapping, ov)
		compat, findings := Analyze(mapped, mapping, nil, nil, qemuFormats)
		if w.Snapshots > 0 {
			findings = append(findings, SnapshotsNotMigrated())
			if compat == CompatReady {
				compat = CompatWarning
			}
		}
		item := ItemPlan{
			SourceID: w.SourceID, Name: w.Name, Kind: w.Kind, Mode: mode, Manifest: mapped,
			Compatibility: compat, Findings: findings, EstimatedBytes: w.EstimatedBytes, StartAfter: startAfter,
		}
		if ov != nil {
			item.OverrideMapping = ov
		}
		if liveAck[w.SourceID] {
			item.LiveAck = true
		}
		p.Items = append(p.Items, item)
	}
	if len(p.Items) == 0 {
		return Plan{}, fmt.Errorf("no workloads selected")
	}
	return p, nil
}

func Review(item ItemPlan, destNode string, mapping Mapping) map[string]any {
	return map[string]any{
		"name":             item.Name,
		"source":           item.Kind + " " + item.SourceID,
		"destination":      item.Kind,
		"destination_node": destNode,
		"migration_mode":   item.Mode,
		"consistency":      modeConsistency(item.Mode),
		"source_safety":    SourceProtected,
		"storage":          mapping.Storage,
		"network":          mapping.Network,
		"compatibility":    item.Compatibility,
		"warnings":         item.Findings,
		"estimated_data":   item.EstimatedBytes,
		"source_changes":   SourceChangesNone(),
		"start_after":      item.StartAfter,
	}
}

func modeConsistency(mode string) string {
	for _, m := range Modes() {
		if m.ID == mode {
			return m.Consistency
		}
	}
	return ConsistencyDepends
}

func NewReport(name, mode string, fields map[string]string, observed []string) Report {
	unobs := []string{VerifyBoot, VerifyGuest, VerifyApp}
	var keep []string
	seen := map[string]struct{}{}
	for _, o := range observed {
		seen[o] = struct{}{}
	}
	for _, u := range unobs {
		if _, ok := seen[u]; !ok {
			keep = append(keep, u)
		}
	}
	dest := "CREATED"
	if _, ok := seen[VerifyBoot]; ok {
		dest = "READY TO BOOT"
	}
	if fields == nil {
		fields = map[string]string{}
	}
	return Report{
		Name: name, Fields: fields, Consistency: modeConsistency(mode),
		SourceSafety: SourceProtected, SourceState: SourceUnchanged(),
		Destination: dest, Observed: observed, Unobserved: keep, SourceChanges: SourceChangesNone(),
	}
}

func WriteBundle(dir string, m Manifest, files map[string]string) error {
	if err := os.MkdirAll(filepath.Join(dir, "disks"), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "rootfs"), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "metadata"), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "checksums"), 0o750); err != nil {
		return err
	}
	sums := map[string]string{}
	for rel, src := range files {
		dst, err := RelJail(dir, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o640); err != nil {
			return err
		}
		sum, _, err := ChecksumFile(dst)
		if err != nil {
			return err
		}
		sums[rel] = sum
	}
	m.SchemaVersion = ManifestSchema
	m.Checksums = sums
	if m.Export == nil {
		m.Export = &ExportMeta{CreatedAt: time.Now().UTC(), Producer: "nodal-ce", Kind: ExportBundle}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0o640); err != nil {
		return err
	}
	sumBody, err := json.MarshalIndent(sums, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "checksums", "sha256.json"), sumBody, 0o640)
}

func ReadBundle(dir string) (Manifest, error) {
	body, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("malformed manifest")
	}
	if m.SchemaVersion != ManifestSchema {
		return Manifest{}, fmt.Errorf("unsupported manifest schema")
	}
	for rel, want := range m.Checksums {
		p, err := RelJail(dir, rel)
		if err != nil {
			return Manifest{}, err
		}
		if err := VerifyChecksum(p, want); err != nil {
			return Manifest{}, err
		}
	}
	return m, nil
}

func (e *Engine) ConvertDisk(ctx context.Context, src, srcFmt, dst, dstFmt string) error {
	argv, err := ConvertArgv(src, srcFmt, dst, dstFmt)
	if err != nil {
		return err
	}
	if e.Convert == nil {
		return fmt.Errorf("qemu-img convert was not run")
	}
	return e.Convert(ctx, argv)
}

func ValidateManifestJSON(body []byte) error {
	if strings.Contains(string(body), "\x00") {
		return fmt.Errorf("malformed manifest")
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("malformed manifest")
	}
	if m.SchemaVersion != ManifestSchema {
		return fmt.Errorf("unsupported manifest schema")
	}
	return nil
}

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o750)
}
