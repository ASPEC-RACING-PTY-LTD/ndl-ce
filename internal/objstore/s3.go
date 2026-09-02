package objstore

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// S3Transport talks to S3-compatible endpoints (R2, AWS, B2, MinIO, generic).
type S3Transport struct {
	HTTP      *http.Client
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	PathStyle bool
}

func (s *S3Transport) client() *http.Client {
	if s != nil && s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *S3Transport) Put(ctx context.Context, bucket, object string, body []byte) error {
	if int64(len(body)) > PartSize {
		return s.putMultipart(ctx, bucket, object, body)
	}
	req, err := s.newRequest(ctx, http.MethodPut, bucket, object, "", body)
	if err != nil {
		return err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		slurp, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("s3 put: %s %s", res.Status, strings.TrimSpace(string(slurp)))
	}
	return nil
}

func (s *S3Transport) Get(ctx context.Context, bucket, object string) ([]byte, error) {
	req, err := s.newRequest(ctx, http.MethodGet, bucket, object, "", nil)
	if err != nil {
		return nil, err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("s3 get: %s", res.Status)
	}
	return io.ReadAll(res.Body)
}

func (s *S3Transport) Head(ctx context.Context, bucket, object string) (bool, int64, error) {
	req, err := s.newRequest(ctx, http.MethodHead, bucket, object, "", nil)
	if err != nil {
		return false, 0, err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return false, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	if res.StatusCode/100 != 2 {
		return false, 0, fmt.Errorf("s3 head: %s", res.Status)
	}
	return true, res.ContentLength, nil
}

func (s *S3Transport) Delete(ctx context.Context, bucket, object string) error {
	req, err := s.newRequest(ctx, http.MethodDelete, bucket, object, "", nil)
	if err != nil {
		return err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("s3 delete: %s", res.Status)
	}
	return nil
}

type initiateResult struct {
	UploadID string `xml:"UploadId"`
}

type completeMultipart struct {
	XMLName xml.Name     `xml:"CompleteMultipartUpload"`
	Parts   []partMarker `xml:"Part"`
}

type partMarker struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (s *S3Transport) putMultipart(ctx context.Context, bucket, object string, body []byte) error {
	initReq, err := s.newRequest(ctx, http.MethodPost, bucket, object, "uploads=", nil)
	if err != nil {
		return err
	}
	initRes, err := s.client().Do(initReq)
	if err != nil {
		return err
	}
	raw, _ := io.ReadAll(io.LimitReader(initRes.Body, 1<<20))
	_ = initRes.Body.Close()
	if initRes.StatusCode/100 != 2 {
		return fmt.Errorf("s3 multipart init: %s %s", initRes.Status, strings.TrimSpace(string(raw)))
	}
	var initiated initiateResult
	if err := xml.Unmarshal(raw, &initiated); err != nil || initiated.UploadID == "" {
		return fmt.Errorf("s3 multipart init: missing upload id")
	}
	var parts []partMarker
	part := 1
	for off := 0; off < len(body); off += PartSize {
		end := off + PartSize
		if end > len(body) {
			end = len(body)
		}
		chunk := body[off:end]
		q := fmt.Sprintf("partNumber=%d&uploadId=%s", part, url.QueryEscape(initiated.UploadID))
		preq, err := s.newRequest(ctx, http.MethodPut, bucket, object, q, chunk)
		if err != nil {
			return err
		}
		pres, err := s.client().Do(preq)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, pres.Body)
		_ = pres.Body.Close()
		if pres.StatusCode/100 != 2 {
			return fmt.Errorf("s3 multipart part %d: %s", part, pres.Status)
		}
		etag := strings.Trim(pres.Header.Get("ETag"), `"`)
		parts = append(parts, partMarker{PartNumber: part, ETag: etag})
		part++
	}
	complete, err := xml.Marshal(completeMultipart{Parts: parts})
	if err != nil {
		return err
	}
	cq := "uploadId=" + url.QueryEscape(initiated.UploadID)
	creq, err := s.newRequest(ctx, http.MethodPost, bucket, object, cq, complete)
	if err != nil {
		return err
	}
	creq.Header.Set("Content-Type", "application/xml")
	cres, err := s.client().Do(creq)
	if err != nil {
		return err
	}
	defer cres.Body.Close()
	if cres.StatusCode/100 != 2 {
		slurp, _ := io.ReadAll(io.LimitReader(cres.Body, 2048))
		return fmt.Errorf("s3 multipart complete: %s %s", cres.Status, strings.TrimSpace(string(slurp)))
	}
	return nil
}

func (s *S3Transport) newRequest(ctx context.Context, method, bucket, object, rawQuery string, body []byte) (*http.Request, error) {
	if s == nil || strings.TrimSpace(s.Endpoint) == "" {
		return nil, fmt.Errorf("s3 endpoint is required")
	}
	if strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return nil, fmt.Errorf("s3 credentials are required")
	}
	base, err := url.Parse(strings.TrimRight(s.Endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("s3 endpoint is invalid")
	}
	u := *base
	if s.PathStyle || !strings.Contains(base.Host, bucket+".") {
		u.Path = "/" + bucket + "/" + object
	} else {
		u.Host = bucket + "." + base.Host
		u.Path = "/" + object
	}
	if rawQuery != "" {
		u.RawQuery = rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	signV4(req, body, s.AccessKey, s.SecretKey, s.Region, "s3", time.Now().UTC())
	return req, nil
}

// NewS3Transport builds a transport for an object-storage target.
func NewS3Transport(endpoint, region, access, secret, provider string, httpClient *http.Client) *S3Transport {
	pathStyle := true
	switch strings.ToLower(provider) {
	case KindAWS:
		pathStyle = false
	}
	return &S3Transport{
		HTTP: httpClient, Endpoint: endpoint, Region: region,
		AccessKey: access, SecretKey: secret, PathStyle: pathStyle,
	}
}
