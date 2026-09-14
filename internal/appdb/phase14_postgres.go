package appdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateGPUAssignment(ctx context.Context, a GPUAssignment) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	existing, err := p.ListGPUAssignments(ctx, a.ClusterID)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.Exclusive || a.Exclusive {
			if e.GPUID == a.GPUID || (a.IOMMUGroup != "" && e.IOMMUGroup == a.IOMMUGroup) {
				return fmt.Errorf("gpu is already exclusively claimed")
			}
		}
	}
	_, err = p.DB.ExecContext(ctx, `
INSERT INTO gpu_assignments (id, cluster_id, gpu_id, workload_id, mode, exclusive, iommu_group, pci_devices, device_nodes, status, reason, created_at)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,COALESCE(string_to_array(NULLIF($8,''), ','), '{}'),COALESCE(string_to_array(NULLIF($9,''), ','), '{}'),$10,$11,$12)`,
		a.ID, a.ClusterID, a.GPUID, a.WorkloadID, a.Mode, a.Exclusive, a.IOMMUGroup,
		strings.Join(a.PCIDevices, ","), strings.Join(a.DeviceNodes, ","), a.Status, a.Reason, a.CreatedAt)
	return err
}

func (p *Postgres) ListGPUAssignments(ctx context.Context, clusterID string) ([]GPUAssignment, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, gpu_id, COALESCE(workload_id::text, ''), mode, exclusive, iommu_group,
       COALESCE(array_to_string(pci_devices, ','), ''), COALESCE(array_to_string(device_nodes, ','), ''),
       status, reason, created_at
FROM gpu_assignments WHERE cluster_id=$1 ORDER BY gpu_id`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GPUAssignment
	for rows.Next() {
		a, err := scanGPUAssignment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (p *Postgres) ListGPUAssignmentsForGPU(ctx context.Context, clusterID, gpuID string) ([]GPUAssignment, error) {
	all, err := p.ListGPUAssignments(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	var out []GPUAssignment
	for _, a := range all {
		if a.GPUID == gpuID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (p *Postgres) GetGPUAssignment(ctx context.Context, clusterID, id string) (*GPUAssignment, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, gpu_id, COALESCE(workload_id::text, ''), mode, exclusive, iommu_group,
       COALESCE(array_to_string(pci_devices, ','), ''), COALESCE(array_to_string(device_nodes, ','), ''),
       status, reason, created_at
FROM gpu_assignments WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	a, err := scanGPUAssignmentRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (p *Postgres) DeleteGPUAssignment(ctx context.Context, clusterID, id string) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM gpu_assignments WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("assignment not found")
	}
	return nil
}

type gpuScanner interface {
	Scan(dest ...any) error
}

func scanGPUAssignment(rows *sql.Rows) (GPUAssignment, error) {
	return scanGPUAssignmentRow(rows)
}

func scanGPUAssignmentRow(row gpuScanner) (GPUAssignment, error) {
	var a GPUAssignment
	var pciCSV, nodeCSV string
	if err := row.Scan(&a.ID, &a.ClusterID, &a.GPUID, &a.WorkloadID, &a.Mode, &a.Exclusive, &a.IOMMUGroup, &pciCSV, &nodeCSV, &a.Status, &a.Reason, &a.CreatedAt); err != nil {
		return a, err
	}
	if pciCSV != "" {
		a.PCIDevices = strings.Split(pciCSV, ",")
	}
	if nodeCSV != "" {
		a.DeviceNodes = strings.Split(nodeCSV, ",")
	}
	return a, nil
}
