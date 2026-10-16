package cloudflare

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
)

// R2Bucket is an R2 bucket in an account.
type R2Bucket struct {
	Name         string `json:"name"`
	CreationDate string `json:"creation_date,omitempty"`
	Location     string `json:"location,omitempty"`
	StorageClass string `json:"storage_class,omitempty"`
}

var r2BucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// R2LocationHints are the location hints Cloudflare accepts.
var R2LocationHints = map[string]bool{"wnam": true, "enam": true, "weur": true, "eeur": true, "apac": true, "oc": true}

// ValidR2BucketName checks Cloudflare's bucket naming rules.
func ValidR2BucketName(name string) error {
	if !r2BucketName.MatchString(name) {
		return fmt.Errorf("bucket names are 3 to 63 lowercase letters, digits or hyphens, starting and ending with a letter or digit")
	}
	return nil
}

// R2Buckets lists an account's R2 buckets. It needs the token's
// Workers R2 Storage Read permission.
func (c *Client) R2Buckets(ctx context.Context, accountID string) ([]R2Bucket, error) {
	if err := validID("account", accountID); err != nil {
		return nil, err
	}
	var out struct {
		Buckets []R2Bucket `json:"buckets"`
	}
	_, err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/r2/buckets", url.Values{"per_page": {"1000"}}, nil, &out)
	return out.Buckets, err
}

// CreateR2Bucket creates a bucket. location is an optional location hint.
// It needs Workers R2 Storage Write.
func (c *Client) CreateR2Bucket(ctx context.Context, accountID, name, location string) (R2Bucket, error) {
	var b R2Bucket
	if err := validID("account", accountID); err != nil {
		return b, err
	}
	if err := ValidR2BucketName(name); err != nil {
		return b, err
	}
	body := map[string]any{"name": name}
	if location != "" {
		if !R2LocationHints[location] {
			return b, fmt.Errorf("unknown location hint %q", location)
		}
		body["locationHint"] = location
	}
	_, err := c.do(ctx, http.MethodPost, "/accounts/"+accountID+"/r2/buckets", nil, body, &b)
	return b, err
}

// DeleteR2Bucket deletes a bucket. Cloudflare refuses a bucket that still
// holds objects, so this never deletes data.
func (c *Client) DeleteR2Bucket(ctx context.Context, accountID, name string) error {
	if err := validID("account", accountID); err != nil {
		return err
	}
	if err := ValidR2BucketName(name); err != nil {
		return err
	}
	_, err := c.do(ctx, http.MethodDelete, "/accounts/"+accountID+"/r2/buckets/"+name, nil, nil, nil)
	return err
}
