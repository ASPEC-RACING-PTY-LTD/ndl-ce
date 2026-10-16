//go:build !unix

package control

import (
	"net"
	"net/http"
)

func appendLocalControlSocket(_ string, _ http.Handler, instances []*httpInstance) []*httpInstance {
	return instances
}

func inheritedTCP(string) net.Listener { return nil }
