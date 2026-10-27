package httpapi

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/gameserver"
)

// playersHost is a running game host that records console lines.
type playersHost struct {
	gameserver.Host
	sent []string
}

func (h *playersHost) Running(context.Context, string) bool { return true }
func (h *playersHost) Send(_ context.Context, _, line string) error {
	h.sent = append(h.sent, line)
	return nil
}

// fakeSrcds answers A2S without challenges, with one player.
func fakeSrcds(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			out := []byte{0xff, 0xff, 0xff, 0xff}
			switch buf[4:n][0] {
			case 'T':
				out = append(out, 'I', 17)
				out = append(out, "JvS\x00rp_jedi_galaxy\x00garrysmod\x00StarWarsRP\x00"...)
				out = append(out, 0xb4, 0x0f, 1, 16, 0)
			case 'U':
				out = append(out, 'D', 1, 0)
				out = append(out, "Rex\x00"...)
				out = binary.LittleEndian.AppendUint32(out, 5)
				out = binary.LittleEndian.AppendUint32(out, math.Float32bits(300))
			}
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func TestGamePlayers(t *testing.T) {
	s, mem, token := testServer(t)
	s.Game = gameserver.NewRuntime(t.TempDir())
	host := &playersHost{Host: &gameserver.LocalHost{RT: s.Game}}
	s.GameHost = host
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	cluster, _ := mem.GetCluster(context.Background())
	enableGameServers(t, mem, cluster.ID)
	port := fakeSrcds(t)
	for id, family := range map[string]string{"gs-gmod": "source", "gs-terra": "terraria"} {
		_ = mem.CreateGameServer(context.Background(), appdb.GameServer{
			ID: id, ClusterID: cluster.ID, Name: id, TemplateID: "ndl-gmod", Family: family, Status: "ready", EnvJSON: []byte(`{}`),
			PortsJSON:    []byte(fmt.Sprintf(`[{"name":"game","container_port":27015,"host_port":%d,"protocol":"udp","primary":true}]`, port)),
			Capabilities: []byte(`["players","console"]`), CreatedAt: time.Now().UTC(),
		})
	}

	code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/game-servers/gs-gmod/players", "")
	if code != 200 || out["supported"] != true || out["running"] != true {
		t.Fatalf("players %d %v", code, out)
	}
	list := out["list"].(map[string]any)
	players := list["players"].([]any)
	if list["map"] != "rp_jedi_galaxy" || list["max"].(float64) != 16 || len(players) != 1 || players[0].(map[string]any)["name"] != "Rex" {
		t.Fatalf("list %v", list)
	}
	if acts := out["actions"].([]any); len(acts) != 1 || acts[0] != "kick" {
		t.Fatalf("actions %v", acts)
	}

	if code, out := jsonCall(t, ts, cookie, "GET", "/api/v1/game-servers/gs-terra/players", ""); code != 200 || out["supported"] != false {
		t.Fatalf("unsupported game %d %v", code, out)
	}

	act := func(body string) int {
		code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/game-servers/gs-gmod/players/action", body)
		return code
	}
	if code := act(`{"action":"kick","name":"Rex"}`); code != 200 || len(host.sent) != 1 || host.sent[0] != `kick "Rex"` {
		t.Fatalf("kick %d %v", code, host.sent)
	}
	for _, body := range []string{
		`{"action":"kick","name":"Rex\"; quit; \""}`,
		`{"action":"kick","name":"Rex\nquit"}`,
		`{"action":"ban","name":"Rex"}`,
	} {
		if code := act(body); code != 400 {
			t.Errorf("%s = %d", body, code)
		}
	}
	if len(host.sent) != 1 {
		t.Fatalf("refused actions reached the console: %v", host.sent)
	}
}
