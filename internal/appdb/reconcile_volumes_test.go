package appdb

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func TestClassifyOrphanVolumesProtectsOperatorAndAttached(t *testing.T) {
	tiny := int64(1024)
	now := time.Now().UTC()
	job := MigrationJob{ID: uuid.NewString(), State: OpStateFailed, Direction: "import", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-30 * time.Minute)}
	opVol := Volume{ID: uuid.NewString(), Class: storage.ClassContainerRoot, Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindOperator, AllocatedBytes: &tiny, CreatedAt: now.Add(-40 * time.Minute)}
	attachedID := uuid.NewString()
	attached := Volume{ID: attachedID, Class: storage.ClassContainerRoot, Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindMigration, OwnerJobID: job.ID, AllocatedBytes: &tiny, CreatedAt: now.Add(-40 * time.Minute)}
	got := ClassifyOrphanVolumes(VolumeFacts{
		Volumes:  []Volume{opVol, attached},
		Attached: map[string]bool{attachedID: true},
		Jobs:     []MigrationJob{job},
		Now:      now,
	})
	if len(got) != 0 {
		t.Fatalf("protected volumes were classified: %+v", got)
	}
}

func TestClassifyOrphanVolumesFindsLegacyEmptyDest(t *testing.T) {
	tiny := int64(1024)
	now := time.Now().UTC()
	job := MigrationJob{ID: uuid.NewString(), State: OpStateFailed, Direction: "import", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-30 * time.Minute)}
	orphan := Volume{
		ID: uuid.NewString(), Class: storage.ClassContainerRoot, AllocatedBytes: &tiny,
		CreatedAt: now.Add(-50 * time.Minute), SizeBytes: 4 << 30,
	}
	got := ClassifyOrphanVolumes(VolumeFacts{Volumes: []Volume{orphan}, Jobs: []MigrationJob{job}, Now: now})
	if len(got) != 1 || got[0].JobID != job.ID {
		t.Fatalf("%+v", got)
	}
}

func TestClassifyOrphanVolumesFindsMarkedMigrationVolume(t *testing.T) {
	tiny := int64(2048)
	now := time.Now().UTC()
	v := Volume{
		ID: uuid.NewString(), Class: storage.ClassContainerRoot,
		Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindMigration, OwnerJobID: uuid.NewString(),
		AllocatedBytes: &tiny, CreatedAt: now.Add(-time.Hour),
	}
	got := ClassifyOrphanVolumes(VolumeFacts{Volumes: []Volume{v}, Now: now})
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestClassifyOrphanVolumesSkipsSucceededJobVolume(t *testing.T) {
	tiny := int64(1024)
	now := time.Now().UTC()
	jobID := uuid.NewString()
	v := Volume{
		ID: uuid.NewString(), Class: storage.ClassContainerRoot,
		Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindMigration, OwnerJobID: jobID,
		AllocatedBytes: &tiny, CreatedAt: now.Add(-time.Hour),
	}
	got := ClassifyOrphanVolumes(VolumeFacts{
		Volumes: []Volume{v},
		Jobs:    []MigrationJob{{ID: jobID, State: OpStateSucceeded}},
		Now:     now,
	})
	if len(got) != 0 {
		t.Fatalf("succeeded dest must be kept: %+v", got)
	}
}
