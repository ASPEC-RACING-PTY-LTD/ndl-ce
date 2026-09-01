package qemu

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type qmpConn struct {
	c net.Conn
	r *bufio.Reader
}

func (e *Engine) dialQMP(id string, timeout time.Duration) (*qmpConn, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", e.qmpPath(id), time.Second)
		if err == nil {
			qc := &qmpConn{c: c, r: bufio.NewReader(c)}
			if err := qc.negotiate(); err != nil {
				_ = c.Close()
				return nil, err
			}
			return qc, nil
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("qmp socket not ready")
	}
	return nil, last
}

func (q *qmpConn) Close() {
	if q != nil && q.c != nil {
		_ = q.c.Close()
	}
}

func (q *qmpConn) negotiate() error {
	if _, err := q.readObject(); err != nil {
		return fmt.Errorf("qmp greeting: %w", err)
	}
	if _, err := q.exec("qmp_capabilities", nil); err != nil {
		return fmt.Errorf("qmp_capabilities: %w", err)
	}
	return nil
}

func (q *qmpConn) exec(cmd string, args map[string]any) (json.RawMessage, error) {
	req := map[string]any{"execute": cmd}
	if len(args) > 0 {
		req["arguments"] = args
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := q.c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	for {
		obj, err := q.readObject()
		if err != nil {
			return nil, err
		}
		if _, ok := obj["return"]; ok {
			raw, _ := json.Marshal(obj["return"])
			return raw, nil
		}
		if ev, ok := obj["error"]; ok {
			return nil, fmt.Errorf("qmp %s: %v", cmd, ev)
		}
	}
}

func (q *qmpConn) readObject() (map[string]any, error) {
	line, err := q.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func (q *qmpConn) queryStatus() (string, error) {
	raw, err := q.exec("query-status", nil)
	if err != nil {
		return "", err
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", err
	}
	return st.Status, nil
}

func (q *qmpConn) powerdown() error {
	_, err := q.exec("system_powerdown", nil)
	return err
}
