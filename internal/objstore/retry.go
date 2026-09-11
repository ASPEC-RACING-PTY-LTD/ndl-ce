package objstore

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func quoteETag(etag string) string {
	etag = strings.TrimSpace(etag)
	etag = strings.Trim(etag, `"`)
	return `"` + etag + `"`
}

func completeMultipartXML(parts []partMarker) []byte {
	var b strings.Builder
	b.WriteString(`<CompleteMultipartUpload>`)
	for _, p := range parts {
		fmt.Fprintf(&b, `<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>`, p.PartNumber, quoteETag(p.ETag))
	}
	b.WriteString(`</CompleteMultipartUpload>`)
	return []byte(b.String())
}

func retryableStatus(code int) bool {
	return code == 429 || code == 500 || code == 502 || code == 503 || code == 504
}

func retryableNet(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(net.Error); ok {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe")
}

func sleepBackoff(ctx context.Context, attempt int) {
	d := time.Duration(1<<attempt) * 250 * time.Millisecond
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Classify maps a transport error to a repository status without echoing secrets.
func Classify(err error) string {
	if err == nil {
		return StatusAvailable
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "signaturedoesnotmatch"), strings.Contains(msg, "invalidaccesskeyid"), strings.Contains(msg, "invalidsecurity"):
		return StatusAuthFailed
	case strings.Contains(msg, "403") && strings.Contains(msg, "access denied"):
		return StatusPermission
	case strings.Contains(msg, "accessdenied"):
		return StatusPermission
	case strings.Contains(msg, "403"):
		return StatusPermission
	case strings.Contains(msg, "nosuchbucket"), strings.Contains(msg, "404"):
		return StatusBucket
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "no such host"), strings.Contains(msg, "tls"):
		return StatusUnavailable
	default:
		return StatusUnavailable
	}
}
