package gameserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net"
	"strconv"
	"testing"
)

func TestQueryTargetFor(t *testing.T) {
	cases := []struct {
		family string
		ports  []Port
		want   QueryTarget
		ok     bool
	}{
		{"source", []Port{{Name: "game", ContainerPort: 27015, Protocol: "udp", Primary: true}, {Name: "rcon", ContainerPort: 27015, HostPort: 27016, Protocol: "tcp"}}, QueryTarget{QueryA2S, 27015}, true},
		{"steam", []Port{{Name: "game", ContainerPort: 28015, Protocol: "udp", Primary: true}, {Name: "query", ContainerPort: 28017, HostPort: 28117, Protocol: "udp"}}, QueryTarget{QueryA2S, 28117}, true},
		{"minecraft", []Port{{Name: "game", ContainerPort: 25565, Primary: true}}, QueryTarget{QueryMinecraft, 25565}, true},
		{"fivem", []Port{{Name: "game", ContainerPort: 30120, Protocol: "udp", Primary: true}, {Name: "game", ContainerPort: 30120, Protocol: "tcp"}}, QueryTarget{QueryFiveM, 30120}, true},
		{"terraria", []Port{{Name: "game", ContainerPort: 7777, Primary: true}}, QueryTarget{}, false},
	}
	for _, c := range cases {
		got, ok := QueryTargetFor(c.family, c.ports)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.family, got, ok, c.want, c.ok)
		}
	}
}

// fakeA2S answers like srcds: a challenge first, then info and a player
// list split over two packets.
func fakeA2S(t *testing.T, players int) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	challenge := []byte{1, 2, 3, 4}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req := buf[4:n]
			send := func(b []byte) { _, _ = pc.WriteTo(append([]byte{0xff, 0xff, 0xff, 0xff}, b...), from) }
			switch {
			case req[0] == 'T' && !bytes.HasSuffix(req, challenge):
				send(append([]byte{'A'}, challenge...))
			case req[0] == 'T':
				info := []byte{'I', 17}
				info = append(info, "JvS\x00rp_jedi_galaxy\x00garrysmod\x00StarWarsRP\x00"...)
				info = append(info, 0xb4, 0x0f, byte(players), 64, 1)
				send(info)
			case req[0] == 'U' && !bytes.Equal(req[1:5], challenge):
				send(append([]byte{'A'}, challenge...))
			case req[0] == 'U':
				body := []byte{0xff, 0xff, 0xff, 0xff, 'D', byte(players + 1)}
				for i := range players {
					body = append(body, byte(i))
					body = append(body, "Player "+strconv.Itoa(i)+"\x00"...)
					body = binary.LittleEndian.AppendUint32(body, uint32(i*10))
					body = binary.LittleEndian.AppendUint32(body, math.Float32bits(float32(i)*60.4))
				}
				// Someone still connecting has no name.
				body = append(body, byte(players), 0)
				body = binary.LittleEndian.AppendUint32(body, 0)
				body = binary.LittleEndian.AppendUint32(body, 0)
				half := len(body) / 2
				for i, part := range [][]byte{body[:half], body[half:]} {
					pkt := []byte{0xfe, 0xff, 0xff, 0xff, 7, 0, 0, 0, 2, byte(i), 0xe0, 0x04}
					_, _ = pc.WriteTo(append(pkt, part...), from)
				}
			}
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

func TestQueryA2S(t *testing.T) {
	port := fakeA2S(t, 3)
	got, err := QueryPlayers(context.Background(), "127.0.0.1", QueryTarget{QueryA2S, port})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "JvS" || got.Map != "rp_jedi_galaxy" || got.Online != 3 || got.Max != 64 || got.Bots != 1 {
		t.Fatalf("info: %+v", got)
	}
	if len(got.Players) != 3 || got.Players[2].Name != "Player 2" || got.Players[2].Score != 20 || got.Players[2].Seconds != 121 {
		t.Fatalf("players: %+v", got.Players)
	}
}

func TestQueryMinecraft(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		for range 2 { // handshake, status request
			n, _ := readVarint(br)
			_, _ = io.CopyN(io.Discard, br, int64(n))
		}
		status, _ := json.Marshal(map[string]any{"players": map[string]any{"max": 20, "online": 3, "sample": []map[string]string{
			{"name": "Steve", "id": "069a79f4-44e9-4726-a5be-fca90e38aaf5"},
			{"name": "Anonymous Player", "id": "00000000-0000-0000-0000-000000000000"},
		}}})
		body := append(varint(0), append(varint(len(status)), status...)...)
		_, _ = c.Write(append(varint(len(body)), body...))
	}()
	got, err := QueryPlayers(context.Background(), "127.0.0.1", QueryTarget{QueryMinecraft, ln.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	if got.Online != 3 || got.Max != 20 || len(got.Players) != 1 || got.Players[0].Name != "Steve" || !got.Partial {
		t.Fatalf("got %+v", got)
	}
}

func TestQueryNobodyHome(t *testing.T) {
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	port := pc.LocalAddr().(*net.UDPAddr).Port
	pc.Close()
	if _, err := QueryPlayers(context.Background(), "127.0.0.1", QueryTarget{QueryA2S, port}); err == nil {
		t.Fatal("a closed port must fail")
	}
}
