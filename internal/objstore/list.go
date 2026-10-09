package objstore

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// ObjectEntry is one listed object.
type ObjectEntry struct {
	Key  string
	Size int64
}

// Lister is implemented by transports that can enumerate a bucket. Backup
// retention uses it to find remote data no restore point needs any more.
type Lister interface {
	List(ctx context.Context, bucket, prefix string) ([]ObjectEntry, error)
}

var (
	_ Lister = (*S3Transport)(nil)
	_ Lister = (*MemoryTransport)(nil)
)

type listBucketResult struct {
	Contents []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

// maxListPages bounds a listing so a misbehaving endpoint cannot loop forever
// (1000 keys per page: ten million objects).
const maxListPages = 10000

// List enumerates every object under prefix with ListObjectsV2.
func (s *S3Transport) List(ctx context.Context, bucket, prefix string) ([]ObjectEntry, error) {
	var out []ObjectEntry
	token := ""
	for page := 0; page < maxListPages; page++ {
		q := url.Values{}
		q.Set("list-type", "2")
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := s.newRequest(ctx, http.MethodGet, bucket, "", q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		res, err := s.client().Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("s3 list: %s %s", res.Status, strings.TrimSpace(string(body[:min(len(body), 2048)])))
		}
		var lr listBucketResult
		if err := xml.Unmarshal(body, &lr); err != nil {
			return nil, fmt.Errorf("s3 list: %w", err)
		}
		for _, c := range lr.Contents {
			out = append(out, ObjectEntry{Key: c.Key, Size: c.Size})
		}
		if !lr.IsTruncated || lr.NextContinuationToken == "" {
			return out, nil
		}
		token = lr.NextContinuationToken
	}
	return nil, fmt.Errorf("s3 list: more than %d pages", maxListPages)
}

// List enumerates objects in the in-memory bucket.
func (m *MemoryTransport) List(_ context.Context, bucket, prefix string) ([]ObjectEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ObjectEntry
	base := bucket + "/"
	for k, v := range m.objects {
		if !strings.HasPrefix(k, base) {
			continue
		}
		key := strings.TrimPrefix(k, base)
		if strings.HasPrefix(key, prefix) {
			out = append(out, ObjectEntry{Key: key, Size: int64(len(v))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
