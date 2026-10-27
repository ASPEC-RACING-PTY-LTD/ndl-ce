package gameserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Live player lists read from the game itself. Steam and Source games
// answer A2S on their query port (the game port for Source), Minecraft
// Java answers the server list ping on its game port, and FiveM serves
// players.json over HTTP on its game port.

// Query protocols.
const (
	QueryA2S       = "a2s"
	QueryMinecraft = "minecraft"
	QueryFiveM     = "fivem"
)

// QueryTarget is where and how to ask a server for its players.
type QueryTarget struct {
	Protocol string
	Port     int
}

// Player is one connected player.
type Player struct {
	Name    string  `json:"name"`
	Score   int     `json:"score,omitempty"`
	Seconds float64 `json:"seconds,omitempty"`
	ID      string  `json:"id,omitempty"`
}

// PlayerList is the answer to a query.
type PlayerList struct {
	Protocol string   `json:"protocol"`
	Name     string   `json:"name,omitempty"`
	Map      string   `json:"map,omitempty"`
	Online   int      `json:"online"`
	Max      int      `json:"max"`
	Bots     int      `json:"bots,omitempty"`
	Players  []Player `json:"players"`
	// Partial is set when the game reports more players than it names
	// (Minecraft sends a sample of at most 12).
	Partial bool `json:"partial,omitempty"`
}

// QueryTargetFor picks the query protocol and host port for a server, or
// false when the game has no player query No-dal speaks.
func QueryTargetFor(family string, ports []Port) (QueryTarget, bool) {
	find := func(match func(Port) bool) int {
		for _, p := range ports {
			if match(p) && HostPortOf(p) > 0 {
				return HostPortOf(p)
			}
		}
		return 0
	}
	proto := func(want string) func(Port) bool {
		return func(p Port) bool {
			got := strings.ToLower(p.Protocol)
			if got == "" {
				got = "tcp"
			}
			return got == want
		}
	}
	primary := func(want string) int {
		if port := find(func(p Port) bool { return p.Primary && proto(want)(p) }); port > 0 {
			return port
		}
		return find(func(p Port) bool { return p.Name == "game" && proto(want)(p) })
	}
	switch strings.ToLower(family) {
	case "minecraft":
		if port := primary("tcp"); port > 0 {
			return QueryTarget{Protocol: QueryMinecraft, Port: port}, true
		}
	case "fivem":
		if port := primary("tcp"); port > 0 {
			return QueryTarget{Protocol: QueryFiveM, Port: port}, true
		}
	case "source", "steam":
		if port := find(func(p Port) bool { return (p.Name == "query" || p.Name == "a2s") && !proto("tcp")(p) }); port > 0 {
			return QueryTarget{Protocol: QueryA2S, Port: port}, true
		}
		if port := primary("udp"); port > 0 {
			return QueryTarget{Protocol: QueryA2S, Port: port}, true
		}
	}
	return QueryTarget{}, false
}

// QueryPlayers asks the server at host for its players.
func QueryPlayers(ctx context.Context, host string, t QueryTarget) (PlayerList, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(t.Port))
	switch t.Protocol {
	case QueryA2S:
		return queryA2S(ctx, addr)
	case QueryMinecraft:
		return queryMinecraft(ctx, addr)
	case QueryFiveM:
		return queryFiveM(ctx, addr)
	}
	return PlayerList{}, fmt.Errorf("no player query for %q", t.Protocol)
}

// ---- A2S (Valve server query) ----

var a2sHeader = []byte{0xff, 0xff, 0xff, 0xff}

func queryA2S(ctx context.Context, addr string) (PlayerList, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return PlayerList{}, err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	out := PlayerList{Protocol: QueryA2S, Players: []Player{}}
	info, err := a2sAsk(conn, append([]byte("TSource Engine Query"), 0), 'I')
	if err != nil {
		return PlayerList{}, err
	}
	if err := parseA2SInfo(info, &out); err != nil {
		return PlayerList{}, err
	}
	players, err := a2sAsk(conn, []byte{'U', 0xff, 0xff, 0xff, 0xff}, 'D')
	if err != nil {
		// Some games turn the player list off; the counts still stand.
		return out, nil
	}
	out.Players = parseA2SPlayers(players)
	return out, nil
}

// a2sAsk sends a request, answers a challenge if the server sends one, and
// returns the reply body after its type byte.
func a2sAsk(conn net.Conn, req []byte, want byte) ([]byte, error) {
	body := req
	for range 3 {
		if _, err := conn.Write(append(append([]byte{}, a2sHeader...), body...)); err != nil {
			return nil, err
		}
		reply, err := a2sRead(conn)
		if err != nil {
			return nil, err
		}
		if len(reply) < 1 {
			return nil, errors.New("empty reply")
		}
		switch reply[0] {
		case want:
			return reply[1:], nil
		case 'A':
			if len(reply) < 5 {
				return nil, errors.New("short challenge")
			}
			// The challenge replaces the placeholder in a player request
			// and is appended to an info request.
			if req[0] == 'U' {
				body = append([]byte{'U'}, reply[1:5]...)
			} else {
				body = append(append([]byte{}, req...), reply[1:5]...)
			}
		default:
			return nil, fmt.Errorf("unexpected reply type 0x%02x", reply[0])
		}
	}
	return nil, errors.New("the server kept sending challenges")
}

// a2sRead reads one reply, putting split packets back together. The body
// returned starts at the type byte.
func a2sRead(conn net.Conn) ([]byte, error) {
	buf := make([]byte, 1400*2)
	var parts map[int][]byte
	total := 0
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		pkt := buf[:n]
		if len(pkt) < 4 {
			continue
		}
		switch int32(binary.LittleEndian.Uint32(pkt)) {
		case -1:
			return append([]byte{}, pkt[4:]...), nil
		case -2:
			// id(4) total(1) number(1) size(2), Source layout.
			if len(pkt) < 12 {
				continue
			}
			if binary.LittleEndian.Uint32(pkt[4:])&0x80000000 != 0 {
				return nil, errors.New("compressed replies are not supported")
			}
			if parts == nil {
				parts = map[int][]byte{}
				total = int(pkt[8])
			}
			parts[int(pkt[9])] = append([]byte{}, pkt[12:]...)
			if total > 0 && len(parts) == total {
				var whole []byte
				for i := range total {
					whole = append(whole, parts[i]...)
				}
				if len(whole) < 4 {
					return nil, errors.New("short split reply")
				}
				return whole[4:], nil
			}
		}
	}
}

type a2sReader struct {
	b   []byte
	err bool
}

func (r *a2sReader) byte() byte {
	if len(r.b) < 1 {
		r.err = true
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *a2sReader) str() string {
	i := bytes.IndexByte(r.b, 0)
	if i < 0 {
		r.err = true
		r.b = nil
		return ""
	}
	v := string(r.b[:i])
	r.b = r.b[i+1:]
	return v
}

func (r *a2sReader) u32() uint32 {
	if len(r.b) < 4 {
		r.err = true
		r.b = nil
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func parseA2SInfo(b []byte, out *PlayerList) error {
	r := a2sReader{b: b}
	r.byte() // protocol
	out.Name = r.str()
	out.Map = r.str()
	r.str() // folder
	r.str() // game
	if len(r.b) >= 2 {
		r.b = r.b[2:] // app id
	}
	out.Online = int(r.byte())
	out.Max = int(r.byte())
	out.Bots = int(r.byte())
	if r.err {
		return errors.New("the server's info reply is malformed")
	}
	return nil
}

func parseA2SPlayers(b []byte) []Player {
	r := a2sReader{b: b}
	n := int(r.byte())
	out := []Player{}
	for range n {
		r.byte() // index
		name := r.str()
		score := int32(r.u32())
		secs := math.Float32frombits(r.u32())
		if r.err {
			break
		}
		// Players still connecting have no name yet.
		if name == "" {
			continue
		}
		out = append(out, Player{Name: name, Score: int(score), Seconds: math.Round(float64(secs))})
	}
	return out
}

// ---- Minecraft Java server list ping ----

func queryMinecraft(ctx context.Context, addr string) (PlayerList, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return PlayerList{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	var hs bytes.Buffer
	hs.Write(varint(0))  // handshake
	hs.Write(varint(-1)) // any protocol version
	hs.Write(varint(len(host)))
	hs.WriteString(host)
	_ = binary.Write(&hs, binary.BigEndian, uint16(port))
	hs.Write(varint(1)) // status
	if _, err := conn.Write(append(varint(hs.Len()), hs.Bytes()...)); err != nil {
		return PlayerList{}, err
	}
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return PlayerList{}, err
	}
	br := bufio.NewReader(conn)
	if _, err := readVarint(br); err != nil { // packet length
		return PlayerList{}, err
	}
	if id, err := readVarint(br); err != nil || id != 0 {
		return PlayerList{}, errors.New("the server did not answer the status request")
	}
	n, err := readVarint(br)
	if err != nil || n <= 0 || n > 1<<20 {
		return PlayerList{}, errors.New("the server's status reply is malformed")
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(br, raw); err != nil {
		return PlayerList{}, err
	}
	var st struct {
		Players struct {
			Max    int `json:"max"`
			Online int `json:"online"`
			Sample []struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"sample"`
		} `json:"players"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return PlayerList{}, errors.New("the server's status reply is malformed")
	}
	out := PlayerList{Protocol: QueryMinecraft, Online: st.Players.Online, Max: st.Players.Max, Players: []Player{}}
	for _, p := range st.Players.Sample {
		// Servers that hide players send a placeholder with a zero UUID.
		if p.Name == "" || p.ID == "00000000-0000-0000-0000-000000000000" {
			continue
		}
		out.Players = append(out.Players, Player{Name: p.Name, ID: p.ID})
	}
	out.Partial = len(out.Players) < out.Online
	return out, nil
}

func varint(v int) []byte {
	u := uint32(v)
	var out []byte
	for {
		if u&^0x7f == 0 {
			return append(out, byte(u))
		}
		out = append(out, byte(u&0x7f|0x80))
		u >>= 7
	}
}

func readVarint(r io.ByteReader) (int, error) {
	var v uint32
	for i := range 5 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int(int32(v)), nil
		}
	}
	return 0, errors.New("varint too long")
}

// ---- FiveM ----

func queryFiveM(ctx context.Context, addr string) (PlayerList, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	get := func(path string, v any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s answered %s", path, resp.Status)
		}
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
	}
	var players []struct {
		Name string `json:"name"`
		ID   int    `json:"id"`
		Ping int    `json:"ping"`
	}
	if err := get("/players.json", &players); err != nil {
		return PlayerList{}, err
	}
	out := PlayerList{Protocol: QueryFiveM, Online: len(players), Players: []Player{}}
	var info struct {
		Vars map[string]string `json:"vars"`
	}
	if get("/info.json", &info) == nil {
		out.Max, _ = strconv.Atoi(info.Vars["sv_maxClients"])
	}
	for _, p := range players {
		out.Players = append(out.Players, Player{Name: p.Name, ID: strconv.Itoa(p.ID)})
	}
	return out, nil
}
