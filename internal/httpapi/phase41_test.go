package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ai"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

type fakeCompleter struct {
	text string
	err  error
	hit  int
}

func (f *fakeCompleter) Complete(context.Context, ai.CompleteRequest) (string, error) {
	f.hit++
	return f.text, f.err
}

func TestPhase41AskCitesEventsAndMetrics(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	s.Observer = fakeObserver{res: metrics.QueryResult{
		Status: metrics.StatusAvailable,
		Series: []metrics.Series{{
			Name: metrics.MetricCPUBusyRatio, Status: metrics.StatusAvailable,
			Points: []metrics.Point{{Time: time.Now().UTC(), Value: 0.91}},
		}},
	}}
	_ = mem.InsertEvent(context.Background(), appdb.Event{
		ID: uuid.NewString(), ClusterID: cluster.ID, Type: "workload.restarted",
		Payload:   json.RawMessage(`{"name":"jellyfin","reason":"guest qemu process exited","api_key":"sk-leakedkeyvalue"}`),
		CreatedAt: time.Now().UTC(),
	})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/ask", strings.NewReader(`{"prompt":"Why did this workload restart?"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ask %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "workload.restarted") || !strings.Contains(string(raw), "cpu.busy_ratio") {
		t.Fatalf("missing citations %s", raw)
	}
	if strings.Contains(string(raw), "sk-leakedkeyvalue") {
		t.Fatalf("secret leaked %s", raw)
	}
	if strings.Contains(string(raw), `"mutate":true`) {
		t.Fatal("ask must not mutate")
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	tasks, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(tasks), `"kind":"workload.migrate"`) {
		t.Fatalf("ask queued a mutate %s", tasks)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health without ai provider %d", res.StatusCode)
	}
}

func TestPhase41ProfileWithoutReadCannotQuery(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/profiles", strings.NewReader(`{"name":"blind","grants":["compute.read"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("profile %d %s", res.StatusCode, raw)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/ask", strings.NewReader(`{"prompt":"Why did this workload restart?","profile_id":"`+id+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(raw), "cannot query") {
		t.Fatalf("blind profile %d %s", res.StatusCode, raw)
	}
}

func TestPhase41ProviderDownStillWorks(t *testing.T) {
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(context.Background())
	seedNode(t, mem, cluster.ID, debianInv(), false)
	_ = mem.InsertEvent(context.Background(), appdb.Event{
		ID: uuid.NewString(), ClusterID: cluster.ID, Type: "workload.restarted",
		Payload: json.RawMessage(`{"name":"jellyfin"}`), CreatedAt: time.Now().UTC(),
	})
	comp := &fakeCompleter{err: errors.New("dial tcp")}
	s.AICompleter = comp
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/providers", strings.NewReader(`{"name":"cloud","kind":"openai_compatible","endpoint":"http://127.0.0.1:9/v1/chat/completions","api_key":"sk-supersecretvalue"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("provider %d %s", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "sk-supersecretvalue") {
		t.Fatalf("key in create json %s", raw)
	}
	var prov map[string]any
	if err := json.Unmarshal(raw, &prov); err != nil {
		t.Fatal(err)
	}
	provID, _ := prov["id"].(string)

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/ai/providers", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	listed, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(listed), "sk-supersecretvalue") || !strings.Contains(string(listed), `"has_credentials":true`) {
		t.Fatalf("list %s", listed)
	}

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/profiles", strings.NewReader(`{"name":"ask","provider_id":"`+provID+`","grants":["events.read","metrics.read"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("ask profile %d %s", res.StatusCode, raw)
	}
	var prof map[string]any
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatal(err)
	}
	profID, _ := prof["id"].(string)

	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/ask", strings.NewReader(`{"prompt":"Why did this workload restart?","profile_id":"`+profID+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"provider_status":"unavailable"`) {
		t.Fatalf("down %d %s", res.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "workload.restarted") {
		t.Fatalf("must still cite %s", raw)
	}
	if strings.Contains(string(raw), "sk-supersecretvalue") {
		t.Fatalf("key in ask %s", raw)
	}
	if comp.hit == 0 {
		t.Fatal("expected completer call")
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("platform health after provider down %d", res.StatusCode)
	}

	hash, err := auth.HashPassword("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := appdb.User{ID: uuid.NewString(), ClusterID: cluster.ID, Username: "view", PasswordHash: hash}
	_ = mem.CreateUser(context.Background(), u)
	_ = mem.BindRole(context.Background(), cluster.ID, u.ID, rbac.Viewer)
	login, err := ts.Client().Post(ts.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"view","password":"password1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var viewCookie string
	for _, c := range login.Cookies() {
		if c.Name == sessionCookie {
			viewCookie = c.Value
		}
	}
	_ = login.Body.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/ai/providers", strings.NewReader(`{"name":"x","kind":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: viewCookie})
	res, _ = ts.Client().Do(req)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer create provider %d", res.StatusCode)
	}
}

func TestPhase41ProviderEndpointRefusesCredentials(t *testing.T) {
	s, _, token := testServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	for _, body := range []string{
		`{"name":"file","kind":"openai_compatible","endpoint":"file:///etc/passwd"}`,
		`{"name":"userinfo","kind":"openai_compatible","endpoint":"https://sk-secret:x@api.example/v1"}`,
	} {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/ai/providers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		res, _ := ts.Client().Do(req)
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("status %d %s for %s", res.StatusCode, raw, body)
		}
		if strings.Contains(string(raw), "sk-secret") {
			t.Fatalf("secret echoed %s", raw)
		}
	}
}
