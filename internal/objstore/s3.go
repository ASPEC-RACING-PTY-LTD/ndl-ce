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
	return defaultS3HTTP()
}

func defaultS3HTTP() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
		},
	}
}

func (s *S3Transport) Put(ctx context.Context, bucket, object string, body []byte) error {
	return s.PutStream(ctx, bucket, object, bytes.NewReader(body), int64(len(body)))
}

func (s *S3Transport) PutStream(ctx context.Context, bucket, object string, r io.Reader, size int64) error {
	if r == nil {
		return fmt.Errorf("s3 put body is required")
	}
	if size >= 0 && size <= int64(PartSize) {
		body, err := io.ReadAll(io.LimitReader(r, int64(PartSize)+1))
		if err != nil {
			return err
		}
		if int64(len(body)) > int64(PartSize) {
			return s.putMultipart(ctx, bucket, object, io.MultiReader(bytes.NewReader(body), r))
		}
		return s.putSingle(ctx, bucket, object, body)
	}
	return s.putMultipart(ctx, bucket, object, r)
}

func (s *S3Transport) putSingle(ctx context.Context, bucket, object string, body []byte) error {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := s.newRequest(ctx, http.MethodPut, bucket, object, "", body)
		if err != nil {
			return err
		}
		res, err := s.client().Do(req)
		if err != nil {
			last = err
			if !retryableNet(err) || attempt == 3 {
				return err
			}
			sleepBackoff(ctx, attempt)
			continue
		}
		slurp, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		_ = res.Body.Close()
		if res.StatusCode/100 == 2 {
			return nil
		}
		last = fmt.Errorf("s3 put: %s %s", res.Status, strings.TrimSpace(string(slurp)))
		if !retryableStatus(res.StatusCode) || attempt == 3 {
			return last
		}
		sleepBackoff(ctx, attempt)
	}
	return last
}

func (s *S3Transport) Get(ctx context.Context, bucket, object string) ([]byte, error) {
	rc, err := s.GetStream(ctx, bucket, object)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, int64(PartSize)*4+1))
}

func (s *S3Transport) GetStream(ctx context.Context, bucket, object string) (io.ReadCloser, error) {
	req, err := s.newRequest(ctx, http.MethodGet, bucket, object, "", nil)
	if err != nil {
		return nil, err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		_ = res.Body.Close()
		return nil, fmt.Errorf("s3 get: %s %s", res.Status, strings.TrimSpace(string(slurp)))
	}
	return res.Body, nil
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
		slurp, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return false, 0, fmt.Errorf("s3 head: %s %s", res.Status, strings.TrimSpace(string(slurp)))
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

type partMarker struct {
	PartNumber int
	ETag       string
}

func (s *S3Transport) putMultipart(ctx context.Context, bucket, object string, r io.Reader) error {
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
	uploadID := initiated.UploadID
	abort := func() { _ = s.abortMultipart(context.Background(), bucket, object, uploadID) }

	buf := make([]byte, PartSize)
	var parts []partMarker
	part := 1
	for {
		n, readErr := io.ReadFull(r, buf)
		if n == 0 && (readErr == io.EOF || readErr == io.ErrUnexpectedEOF) {
			break
		}
		if n == 0 && readErr != nil {
			abort()
			return readErr
		}
		etag, err := s.uploadPart(ctx, bucket, object, uploadID, part, buf[:n])
		if err != nil {
			abort()
			return err
		}
		parts = append(parts, partMarker{PartNumber: part, ETag: etag})
		part++
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			abort()
			return readErr
		}
	}
	if len(parts) == 0 {
		abort()
		return fmt.Errorf("s3 multipart: empty body")
	}
	complete := completeMultipartXML(parts)
	cq := "uploadId=" + url.QueryEscape(uploadID)
	creq, err := s.newRequest(ctx, http.MethodPost, bucket, object, cq, complete)
	if err != nil {
		abort()
		return err
	}
	creq.Header.Set("Content-Type", "application/xml")
	cres, err := s.client().Do(creq)
	if err != nil {
		abort()
		return err
	}
	slurp, _ := io.ReadAll(io.LimitReader(cres.Body, 2048))
	_ = cres.Body.Close()
	if cres.StatusCode/100 != 2 {
		abort()
		return fmt.Errorf("s3 multipart complete: %s %s", cres.Status, strings.TrimSpace(string(slurp)))
	}
	return nil
}

func (s *S3Transport) uploadPart(ctx context.Context, bucket, object, uploadID string, part int, chunk []byte) (string, error) {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		q := fmt.Sprintf("partNumber=%d&uploadId=%s", part, url.QueryEscape(uploadID))
		preq, err := s.newRequest(ctx, http.MethodPut, bucket, object, q, chunk)
		if err != nil {
			return "", err
		}
		pres, err := s.client().Do(preq)
		if err != nil {
			last = err
			if !retryableNet(err) || attempt == 3 {
				return "", err
			}
			sleepBackoff(ctx, attempt)
			continue
		}
		etag := pres.Header.Get("ETag")
		_, _ = io.Copy(io.Discard, pres.Body)
		_ = pres.Body.Close()
		if pres.StatusCode/100 == 2 {
			if strings.TrimSpace(etag) == "" {
				return "", fmt.Errorf("s3 multipart part %d: missing etag", part)
			}
			return etag, nil
		}
		last = fmt.Errorf("s3 multipart part %d: %s", part, pres.Status)
		if !retryableStatus(pres.StatusCode) || attempt == 3 {
			return "", last
		}
		sleepBackoff(ctx, attempt)
	}
	return "", last
}

func (s *S3Transport) abortMultipart(ctx context.Context, bucket, object, uploadID string) error {
	q := "uploadId=" + url.QueryEscape(uploadID)
	req, err := s.newRequest(ctx, http.MethodDelete, bucket, object, q, nil)
	if err != nil {
		return err
	}
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("s3 multipart abort: %s", res.Status)
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
	if httpClient == nil {
		httpClient = defaultS3HTTP()
	}
	return &S3Transport{
		HTTP: httpClient, Endpoint: endpoint, Region: region,
		AccessKey: access, SecretKey: secret, PathStyle: pathStyle,
	}
}
