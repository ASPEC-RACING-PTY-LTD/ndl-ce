package httpapi

import (
	"crypto/subtle"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
)

// A terminal shell outlives its browser socket: when the socket drops (a
// page refresh, a network blip) the shell keeps running for termDetachGrace,
// its recent output is kept, and the page reattaches with a fresh one-time
// ticket from POST /io/sessions/{id}/attach. Closing the tab ends it at once.

var (
	termDetachGrace     = 10 * time.Minute
	termReplayBytes     = 256 << 10
	termAttachTicketTTL = time.Minute
	termWriteWait       = 45 * time.Second
)

type liveTerm struct {
	id     string
	conn   agentrpc.TermConn
	onEnd  func()
	mu     sync.Mutex
	frames [][]byte
	size   int
	ws     *websocket.Conn
	// cur mirrors ws so attach can close a stalled socket without waiting
	// for pump's write (which holds mu) to time out.
	cur   atomic.Pointer[websocket.Conn]
	timer *time.Timer
	ended bool
	// attachHash is the hashed one-time ticket for the next reattach.
	attachHash string
	attachExp  time.Time
}

var liveTerms = struct {
	mu sync.Mutex
	m  map[string]*liveTerm
}{m: map[string]*liveTerm{}}

func getLiveTerm(id string) *liveTerm {
	liveTerms.mu.Lock()
	defer liveTerms.mu.Unlock()
	return liveTerms.m[id]
}

// startLiveTerm registers a shell and pumps its output.
func startLiveTerm(id string, conn agentrpc.TermConn, onEnd func()) *liveTerm {
	lt := &liveTerm{id: id, conn: conn, onEnd: onEnd}
	liveTerms.mu.Lock()
	liveTerms.m[id] = lt
	liveTerms.mu.Unlock()
	go lt.pump()
	return lt
}

func (lt *liveTerm) pump() {
	for {
		data, err := lt.conn.Recv()
		if err != nil {
			lt.end()
			return
		}
		lt.mu.Lock()
		lt.frames = append(lt.frames, data)
		lt.size += len(data)
		for lt.size > termReplayBytes && len(lt.frames) > 1 {
			lt.size -= len(lt.frames[0])
			lt.frames = lt.frames[1:]
		}
		if lt.ws != nil {
			// A burst (docker build redrawing its progress) can stall a slow
			// link for a while; give it time before dropping the socket. The
			// page reattaches and the replay fills the gap either way.
			_ = lt.ws.SetWriteDeadline(time.Now().Add(termWriteWait))
			if lt.ws.WriteMessage(websocket.BinaryMessage, data) != nil {
				_ = lt.ws.Close()
			}
		}
		lt.mu.Unlock()
	}
}

// attach makes ws the shell's socket, replaying recent output first.
func (lt *liveTerm) attach(ws *websocket.Conn) bool {
	if old := lt.cur.Load(); old != nil && old != ws {
		_ = old.Close()
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if lt.ended {
		return false
	}
	if lt.timer != nil {
		lt.timer.Stop()
		lt.timer = nil
	}
	if lt.ws != nil && lt.ws != ws {
		_ = lt.ws.Close()
	}
	lt.ws = ws
	lt.cur.Store(ws)
	for _, f := range lt.frames {
		_ = ws.SetWriteDeadline(time.Now().Add(termWriteWait))
		if ws.WriteMessage(websocket.BinaryMessage, f) != nil {
			break
		}
	}
	return true
}

// detach forgets ws and starts the grace period.
func (lt *liveTerm) detach(ws *websocket.Conn) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	if lt.ws != ws || lt.ended {
		return
	}
	lt.ws = nil
	lt.cur.Store(nil)
	lt.timer = time.AfterFunc(termDetachGrace, lt.end)
}

func (lt *liveTerm) end() {
	lt.mu.Lock()
	if lt.ended {
		lt.mu.Unlock()
		return
	}
	lt.ended = true
	if lt.timer != nil {
		lt.timer.Stop()
	}
	ws := lt.ws
	lt.ws = nil
	lt.cur.Store(nil)
	lt.mu.Unlock()
	liveTerms.mu.Lock()
	if liveTerms.m[lt.id] == lt {
		delete(liveTerms.m, lt.id)
	}
	liveTerms.mu.Unlock()
	_ = lt.conn.Close()
	if ws != nil {
		_ = ws.Close()
	}
	if lt.onEnd != nil {
		lt.onEnd()
	}
}

func (lt *liveTerm) setAttachTicket(hash string, now time.Time) {
	lt.mu.Lock()
	lt.attachHash, lt.attachExp = hash, now.Add(termAttachTicketTTL)
	lt.mu.Unlock()
}

// takeAttachTicket consumes the reattach ticket if hash matches it.
func (lt *liveTerm) takeAttachTicket(hash string, now time.Time) bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	ok := lt.attachHash != "" && now.Before(lt.attachExp) &&
		subtle.ConstantTimeCompare([]byte(lt.attachHash), []byte(hash)) == 1
	if ok {
		lt.attachHash = ""
	}
	return ok
}

// serveLiveTerm copies the socket's input to the shell until the socket
// closes, then detaches.
func serveLiveTerm(lt *liveTerm, ws *websocket.Conn) {
	if !lt.attach(ws) {
		return
	}
	defer lt.detach(ws)
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if err := lt.conn.Send(data); err != nil {
			return
		}
	}
}
