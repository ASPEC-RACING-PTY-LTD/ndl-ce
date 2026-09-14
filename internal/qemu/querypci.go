package qemu

import (
	"encoding/json"
	"fmt"
	"time"
)

type pciDevice struct {
	QDevID   string `json:"qdev_id"`
	Bus      string `json:"bus"`
	Slot     int    `json:"slot"`
	Function int    `json:"function"`
	Class    string `json:"class_info"`
	Addr     string `json:"addr"`
}

func (q *qmpConn) queryPCI() ([]pciDevice, error) {
	raw, err := q.exec("query-pci", nil)
	if err != nil {
		return nil, err
	}
	var buses []struct {
		Devices []struct {
			QDevID string `json:"qdev_id"`
			Slot   int    `json:"slot"`
			Fun    int    `json:"function"`
			Class  struct {
				Desc string `json:"desc"`
			} `json:"class_info"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &buses); err != nil {
		return nil, err
	}
	var out []pciDevice
	for _, b := range buses {
		for _, d := range b.Devices {
			out = append(out, pciDevice{
				QDevID: d.QDevID, Slot: d.Slot, Function: d.Fun,
				Class: d.Class.Desc, Addr: fmt.Sprintf("0x%x", d.Slot),
			})
		}
	}
	return out, nil
}

func (e *Engine) QueryPCI(id string) ([]pciDevice, error) {
	if e.SkipHostCmds {
		applied, err := e.ReadApplied(id)
		if err != nil {
			return nil, err
		}
		var out []pciDevice
		for key, addr := range applied.Launch.PCI {
			out = append(out, pciDevice{QDevID: key, Addr: addr})
		}
		return out, nil
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	return q.queryPCI()
}

func pciMatchesLaunch(live []pciDevice, want map[string]string) bool {
	have := map[string]struct{}{}
	for _, d := range live {
		if d.Addr != "" {
			have[d.Addr] = struct{}{}
		}
	}
	for _, addr := range want {
		if _, ok := have[addr]; !ok {
			return false
		}
	}
	return true
}
