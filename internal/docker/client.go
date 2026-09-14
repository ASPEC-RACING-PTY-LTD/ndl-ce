package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const dockerAPIPrefix = "/v1.41"

type unixClient struct {
	socket string
	http   *http.Client
}

var _ engineAPI = (*unixClient)(nil)

func newUnixClient(socket string) *unixClient {
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	return &unixClient{
		socket: socket,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socket)
				},
				ResponseHeaderTimeout: 20 * time.Second,
			},
		},
	}
}

func (c *unixClient) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+dockerAPIPrefix+path, rdr)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<10))
		_ = res.Body.Close()
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = res.Status
		}
		return nil, fmt.Errorf("docker API %s %s: %s", method, path, compactDockerError(msg))
	}
	return res, nil
}

func (c *unixClient) json(ctx context.Context, method, path string, body []byte, out any) error {
	res, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *unixClient) ping(ctx context.Context) (string, error) {
	var ver struct {
		Version string `json:"Version"`
	}
	if err := c.json(ctx, http.MethodGet, "/version", nil, &ver); err != nil {
		return "", err
	}
	return ver.Version, nil
}

type listItem struct {
	ID         string            `json:"Id"`
	Names      []string          `json:"Names"`
	Image      string            `json:"Image"`
	ImageID    string            `json:"ImageID"`
	State      string            `json:"State"`
	Status     string            `json:"Status"`
	Labels     map[string]string `json:"Labels"`
	Ports      []apiPort         `json:"Ports"`
	Mounts     []apiMount        `json:"Mounts"`
	Created    int64             `json:"Created"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type inspectJSON struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Image   string `json:"Image"`
	State   struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		OOMKilled  bool   `json:"OOMKilled"`
		Dead       bool   `json:"Dead"`
		Pid        int    `json:"Pid"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				Output   string `json:"Output"`
				ExitCode int    `json:"ExitCode"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	RestartCount int `json:"RestartCount"`
	Config       struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig      json.RawMessage `json:"HostConfig"`
	NetworkSettings struct {
		IPAddress string `json:"IPAddress"`
		Ports     map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	Mounts []apiMount `json:"Mounts"`
}

type apiPort struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type apiMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
	Mode        string `json:"Mode"`
}

type statsJSON struct {
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint32 `json:"online_cpus"`
}

type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Status string `json:"status"`
	ID     string `json:"id"`
	From   string `json:"from"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time     int64 `json:"time"`
	TimeNano int64 `json:"timeNano"`
}

func (c *unixClient) listContainers(ctx context.Context) ([]listItem, error) {
	var items []listItem
	if err := c.json(ctx, http.MethodGet, "/containers/json?all=1", nil, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *unixClient) inspect(ctx context.Context, id string) (inspectJSON, error) {
	var out inspectJSON
	err := c.json(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &out)
	return out, err
}

func (c *unixClient) stats(ctx context.Context, id string) (statsJSON, error) {
	var out statsJSON
	err := c.json(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats?stream=0&one-shot=true", nil, &out)
	return out, err
}

func (c *unixClient) start(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
}

func (c *unixClient) stop(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop?t=10", nil, nil)
}

func (c *unixClient) restart(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart?t=10", nil, nil)
}

func (c *unixClient) logs(ctx context.Context, id string, tail int) (string, error) {
	if tail <= 0 {
		tail = 200
	}
	if tail > 2000 {
		tail = 2000
	}
	path := fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&timestamps=1&tail=%d", url.PathEscape(id), tail)
	res, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return decodeDockerLogs(raw), nil
}

func (c *unixClient) pull(ctx context.Context, image string) error {
	from, tag := splitImage(image)
	q := "/images/create?fromImage=" + url.QueryEscape(from)
	if tag != "" {
		q += "&tag=" + url.QueryEscape(tag)
	}
	res, err := c.do(ctx, http.MethodPost, q, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	for {
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if msg, _ := line["error"].(string); strings.TrimSpace(msg) != "" {
			return fmt.Errorf("image pull failed: %s", msg)
		}
	}
}

func (c *unixClient) eventsSince(ctx context.Context, since, until time.Time) ([]dockerEvent, error) {
	q := fmt.Sprintf("/events?since=%d&until=%d", since.Unix(), until.Unix())
	res, err := c.do(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out []dockerEvent
	sc := bufio.NewScanner(io.LimitReader(res.Body, 2<<20))
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev dockerEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (c *unixClient) recreate(ctx context.Context, id string) error {
	res, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	_ = res.Body.Close()
	if err != nil {
		return err
	}
	var ins struct {
		ID              string          `json:"Id"`
		Name            string          `json:"Name"`
		Config          map[string]any  `json:"Config"`
		HostConfig      json.RawMessage `json:"HostConfig"`
		NetworkSettings struct {
			Networks map[string]any `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(raw, &ins); err != nil {
		return err
	}
	name := strings.TrimPrefix(ins.Name, "/")
	if name == "" {
		return fmt.Errorf("container name is missing")
	}
	image, _ := ins.Config["Image"].(string)
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("container image is missing")
	}
	if err := c.pull(ctx, image); err != nil {
		return err
	}
	_ = c.stop(ctx, id)
	tmp := name + "-ndl-old"
	if err := c.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(tmp), nil, nil); err != nil {
		return err
	}
	rollback := func() {
		_ = c.json(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(name), nil, nil)
		_ = c.start(ctx, id)
	}
	payload := map[string]any{}
	for k, v := range ins.Config {
		payload[k] = v
	}
	if len(ins.HostConfig) > 0 {
		var hc any
		if err := json.Unmarshal(ins.HostConfig, &hc); err == nil {
			payload["HostConfig"] = hc
		}
	}
	if len(ins.NetworkSettings.Networks) > 0 {
		payload["NetworkingConfig"] = map[string]any{"EndpointsConfig": ins.NetworkSettings.Networks}
	}
	createBody, err := json.Marshal(payload)
	if err != nil {
		rollback()
		return err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.json(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), createBody, &created); err != nil {
		rollback()
		return err
	}
	if err := c.start(ctx, created.ID); err != nil {
		_ = c.json(ctx, http.MethodDelete, "/containers/"+url.PathEscape(created.ID)+"?force=1", nil, nil)
		rollback()
		return err
	}
	_ = c.json(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1", nil, nil)
	return nil
}

func splitImage(image string) (name, tag string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", "latest"
	}
	if i := strings.LastIndex(image, ":"); i > 0 && !strings.Contains(image[i:], "/") {
		return image[:i], image[i+1:]
	}
	return image, "latest"
}

func compactDockerError(raw string) string {
	raw = strings.TrimSpace(raw)
	var obj struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal([]byte(raw), &obj) == nil {
		if obj.Message != "" {
			return obj.Message
		}
		if obj.Error != "" {
			return obj.Error
		}
	}
	if len(raw) > 400 {
		return raw[:400]
	}
	return raw
}

func decodeDockerLogs(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) < 8 || raw[0] > 2 {
		return strings.TrimRight(string(raw), "\x00")
	}
	var b strings.Builder
	for i := 0; i+8 <= len(raw); {
		stream := raw[i]
		if stream > 2 {
			return strings.TrimRight(string(raw), "\x00")
		}
		n := int(raw[i+4])<<24 | int(raw[i+5])<<16 | int(raw[i+6])<<8 | int(raw[i+7])
		i += 8
		if n < 0 || i+n > len(raw) {
			b.Write(raw[i:])
			break
		}
		b.Write(raw[i : i+n])
		i += n
	}
	return b.String()
}

func parseDockerTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0001-01-01") {
		return time.Time{}, fmt.Errorf("empty")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	return time.Parse(time.RFC3339, s)
}

func cpuPercent(s statsJSON) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemCPUUsage) - float64(s.PreCPUStats.SystemCPUUsage)
	ncpu := float64(s.CPUStats.OnlineCPUs)
	if ncpu <= 0 {
		ncpu = 1
	}
	if cpuDelta > 0 && sysDelta > 0 {
		return (cpuDelta / sysDelta) * ncpu * 100
	}
	return 0
}

func formatUptime(started string, now time.Time) string {
	t, err := parseDockerTime(started)
	if err != nil || t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}
