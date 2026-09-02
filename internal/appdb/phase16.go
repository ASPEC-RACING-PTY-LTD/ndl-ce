package appdb

import (
	"context"
	"sort"
	"time"
)

const (
	AlertOpGT           = "gt"
	AlertOpLT           = "lt"
	NotifyWebhook       = "webhook"
	NotifySMTP          = "smtp"
	NotifyConfigured    = "configured"
	NotifyNotConfigured = "not_configured"
)

// AlertRule evaluates one metric against a threshold and writes events when it fires.
type AlertRule struct {
	ID          string
	ClusterID   string
	Name        string
	Metric      string
	Op          string
	Threshold   float64
	ForMinutes  int
	Enabled     bool
	LastFiredAt *time.Time
	CreatedAt   time.Time
}

// NotificationChannel is a local webhook or optional SMTP destination.
// Webhook URLs and SMTP passwords are never stored on this row.
type NotificationChannel struct {
	ID           string
	ClusterID    string
	Name         string
	Kind         string
	SMTPHost     string
	SMTPPort     int
	SMTPFrom     string
	SMTPUsername string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (m *Memory) CreateAlertRule(_ context.Context, r AlertRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alertRules == nil {
		m.alertRules = map[string]AlertRule{}
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	m.alertRules[r.ID] = r
	return nil
}

func (m *Memory) ListAlertRules(_ context.Context, clusterID string) ([]AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AlertRule
	for _, r := range m.alertRules {
		if r.ClusterID == clusterID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetAlertRule(_ context.Context, clusterID, id string) (*AlertRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[id]
	if !ok || r.ClusterID != clusterID {
		return nil, nil
	}
	cp := r
	return &cp, nil
}

func (m *Memory) UpdateAlertRuleFired(_ context.Context, clusterID, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.alertRules[id]
	if !ok || r.ClusterID != clusterID {
		return nil
	}
	r.LastFiredAt = &at
	m.alertRules[id] = r
	return nil
}

func (m *Memory) CreateNotificationChannel(_ context.Context, c NotificationChannel, webhookURL, smtpPassword string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyChannels == nil {
		m.notifyChannels = map[string]NotificationChannel{}
	}
	if m.notifySecrets == nil {
		m.notifySecrets = map[string][2]string{}
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	c.UpdatedAt = c.CreatedAt
	m.notifyChannels[c.ID] = c
	m.notifySecrets[c.ID] = [2]string{webhookURL, smtpPassword}
	return nil
}

func (m *Memory) ListNotificationChannels(_ context.Context, clusterID string) ([]NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []NotificationChannel
	for _, c := range m.notifyChannels {
		if c.ClusterID == clusterID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) GetNotificationChannel(_ context.Context, clusterID, id string) (*NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.notifyChannels[id]
	if !ok || c.ClusterID != clusterID {
		return nil, nil
	}
	cp := c
	return &cp, nil
}

func (m *Memory) NotificationSecrets(_ context.Context, clusterID, id string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.notifyChannels[id]
	if !ok || c.ClusterID != clusterID {
		return "", "", nil
	}
	sec := m.notifySecrets[id]
	return sec[0], sec[1], nil
}
