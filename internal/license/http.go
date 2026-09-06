package license

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPProbe POSTs the key to the licensing API. It is unused until a key is entered.
type HTTPProbe struct {
	Client   *http.Client
	Endpoint string
	Trust    TrustBundle
	Cluster  string
	Install  string
	Version  string
}

func (h HTTPProbe) Check(ctx context.Context, key string) error {
	_, err := h.Activate(ctx, key, ActivationRequest{Edition: EditionCE, ClusterID: h.Cluster, InstallationID: h.Install, CEVersion: h.Version})
	return err
}

func (h HTTPProbe) Activate(ctx context.Context, key string, req ActivationRequest) (*Document, error) {
	if key == "" {
		return nil, nil
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	endpoint := h.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if req.Edition == "" {
		req.Edition = EditionCE
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	res, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("%w", ErrUnreachable)
	}
	doc, ok := ParseDocument(raw)
	trust := h.Trust
	if trust == nil {
		trust = LoadTrust("")
	}
	if ok && strings.TrimSpace(doc.Signature) != "" {
		if err := Verify(doc, trust); err != nil {
			return &doc, fmt.Errorf("%w", ErrNotEntitled)
		}
		if doc.WorkloadsStopped {
			doc.WorkloadsStopped = false
		}
		return &doc, nil
	}
	if !entitlementGranted(raw) {
		return &doc, fmt.Errorf("%w", ErrNotEntitled)
	}
	if !ok {
		doc = Document{Accepted: true, Entitled: true, Edition: EditionCE, WorkloadsStopped: false}
	}
	return &doc, nil
}

func entitlementGranted(raw []byte) bool {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	if b, ok := asBool(payload["accepted"]); ok && b {
		return true
	}
	if b, ok := asBool(payload["entitled"]); ok && b {
		return true
	}
	return false
}

func asBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}
