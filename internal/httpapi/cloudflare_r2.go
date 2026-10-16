package httpapi

import (
	"net/http"
	"sort"
	"strings"

	cf "github.com/no-dal/ndl-ce/internal/cloudflare"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// R2 bucket management uses the Cloudflare API tokens saved on the
// Cloudflare page. Tokens stay sealed on the server; only bucket names,
// locations and errors are returned.

// listR2Buckets lists the R2 buckets of every account a saved token reaches.
func (s *Server) listR2Buckets(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareRead)
	if err != nil {
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	creds, _ := s.Store.ListCloudflareCredentials(ctx, clusterID)
	accounts := map[string]string{}
	for _, c := range creds {
		if c.Status == cfStatusInvalid {
			continue
		}
		for _, sc := range cfCredScopes(c) {
			accounts[sc.Account.ID] = sc.Account.Name
		}
	}
	ids := make([]string, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []map[string]any{}
	for _, id := range ids {
		row := map[string]any{"account_id": id, "account_name": accounts[id], "buckets": []cf.R2Bucket{}}
		var buckets []cf.R2Bucket
		err := s.cfWith(ctx, clusterID, id, "", func(c *cf.Client) error {
			var e error
			buckets, e = c.R2Buckets(ctx, id)
			return e
		})
		if err != nil {
			row["error"] = r2ErrText(err)
		} else {
			sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
			row["buckets"] = buckets
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) createR2Bucket(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
		Location  string `json:"location"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := cf.ValidR2BucketName(in.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	var b cf.R2Bucket
	if err := s.cfWith(ctx, clusterID, in.AccountID, "", func(c *cf.Client) error {
		var e error
		b, e = c.CreateR2Bucket(ctx, in.AccountID, in.Name, strings.TrimSpace(in.Location))
		return e
	}); err != nil {
		writeErr(w, cfHTTPStatus(err), r2ErrText(err))
		return
	}
	s.audit(r, clusterID, p.User.ID, "cloudflare.r2.bucket.create", "ok", in.AccountID+" "+in.Name)
	writeJSON(w, http.StatusCreated, b)
}

func (s *Server) deleteR2Bucket(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.CloudflareManage)
	if err != nil {
		return
	}
	var in struct {
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, clusterID := r.Context(), p.User.ClusterID
	if err := s.cfWith(ctx, clusterID, in.AccountID, "", func(c *cf.Client) error {
		return c.DeleteR2Bucket(ctx, in.AccountID, in.Name)
	}); err != nil {
		writeErr(w, cfHTTPStatus(err), r2ErrText(err))
		return
	}
	s.audit(r, clusterID, p.User.ID, "cloudflare.r2.bucket.delete", "ok", in.AccountID+" "+in.Name)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// r2ErrText explains the usual causes of a refused bucket call.
func r2ErrText(err error) string {
	m := err.Error()
	low := strings.ToLower(m)
	switch {
	case strings.Contains(low, "not empty"):
		return m + " (empty the bucket first; buckets with objects are never deleted here)"
	case strings.Contains(low, "auth") || strings.Contains(low, "permission") || strings.Contains(low, "403"):
		return m + " (give the Cloudflare token Workers R2 Storage Read, or Edit to create and delete buckets)"
	}
	return m
}
