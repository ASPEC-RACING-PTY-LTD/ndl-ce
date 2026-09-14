package appdb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (p *Postgres) CreateAlertRule(ctx context.Context, r AlertRule) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := p.DB.ExecContext(ctx, `
INSERT INTO alert_rules (id, cluster_id, name, metric, op, threshold, for_minutes, enabled, last_fired_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		r.ID, r.ClusterID, r.Name, r.Metric, r.Op, r.Threshold, r.ForMinutes, r.Enabled, r.LastFiredAt, r.CreatedAt)
	return err
}

func (p *Postgres) ListAlertRules(ctx context.Context, clusterID string) ([]AlertRule, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, metric, op, threshold, for_minutes, enabled, last_fired_at, created_at
FROM alert_rules WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) GetAlertRule(ctx context.Context, clusterID, id string) (*AlertRule, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, metric, op, threshold, for_minutes, enabled, last_fired_at, created_at
FROM alert_rules WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	r, err := scanAlertRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (p *Postgres) UpdateAlertRuleFired(ctx context.Context, clusterID, id string, at time.Time) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE alert_rules SET last_fired_at=$3 WHERE cluster_id=$1 AND id=$2`, clusterID, id, at)
	return err
}

func (p *Postgres) CreateNotificationChannel(ctx context.Context, c NotificationChannel, webhookURL, smtpPassword string) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO notification_channels (id, cluster_id, name, kind, smtp_host, smtp_port, smtp_from, smtp_username, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`,
		c.ID, c.ClusterID, c.Name, c.Kind, c.SMTPHost, c.SMTPPort, c.SMTPFrom, c.SMTPUsername, c.Status, c.CreatedAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO secrets.notification_secrets (channel_id, cluster_id, webhook_url, smtp_password, updated_at)
VALUES ($1,$2,$3,$4,$5)`, c.ID, c.ClusterID, webhookURL, smtpPassword, c.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (p *Postgres) ListNotificationChannels(ctx context.Context, clusterID string) ([]NotificationChannel, error) {
	rows, err := p.DB.QueryContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, smtp_host, smtp_port, smtp_from, smtp_username, status, created_at, updated_at
FROM notification_channels WHERE cluster_id=$1 ORDER BY created_at ASC`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		c, err := scanNotifyChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *Postgres) GetNotificationChannel(ctx context.Context, clusterID, id string) (*NotificationChannel, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT id::text, cluster_id::text, name, kind, smtp_host, smtp_port, smtp_from, smtp_username, status, created_at, updated_at
FROM notification_channels WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	c, err := scanNotifyChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (p *Postgres) NotificationSecrets(ctx context.Context, clusterID, id string) (string, string, error) {
	row := p.DB.QueryRowContext(ctx, `
SELECT webhook_url, smtp_password FROM secrets.notification_secrets WHERE cluster_id=$1 AND channel_id=$2`, clusterID, id)
	var url, pass string
	if err := row.Scan(&url, &pass); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	return url, pass, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAlertRule(s scanner) (AlertRule, error) {
	var r AlertRule
	var fired sql.NullTime
	if err := s.Scan(&r.ID, &r.ClusterID, &r.Name, &r.Metric, &r.Op, &r.Threshold, &r.ForMinutes, &r.Enabled, &fired, &r.CreatedAt); err != nil {
		return r, err
	}
	if fired.Valid {
		t := fired.Time.UTC()
		r.LastFiredAt = &t
	}
	return r, nil
}

func scanNotifyChannel(s scanner) (NotificationChannel, error) {
	var c NotificationChannel
	if err := s.Scan(&c.ID, &c.ClusterID, &c.Name, &c.Kind, &c.SMTPHost, &c.SMTPPort, &c.SMTPFrom, &c.SMTPUsername, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	return c, nil
}
