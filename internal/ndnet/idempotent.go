package ndnet

import (
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) persistMatches(plan Plan) bool {
	for _, file := range e.persistFiles(plan) {
		path := filepath.Join(e.networkDir(), filepath.Base(file.RelPath))
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		if strings.TrimSpace(string(b)) != strings.TrimSpace(file.Body) {
			return false
		}
	}
	return len(e.persistFiles(plan)) > 0
}

func lanBridgeLive(plan Plan, host HostView) bool {
	if plan.Kind != KindLANBridge {
		return false
	}
	br, ok := lookup(host, plan.BridgeName)
	if !ok || !br.Up {
		return false
	}
	up, ok := lookup(host, plan.UplinkIfName)
	if !ok {
		return false
	}
	return sameIface(up.Master, plan.BridgeName)
}

func (e *Engine) lanAlreadyApplied(plan Plan, host HostView) bool {
	if plan.Kind != KindLANBridge {
		return false
	}
	return e.persistMatches(plan) && lanBridgeLive(plan, host)
}
