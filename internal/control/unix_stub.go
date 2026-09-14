//go:build !unix

package control

import "net/http"

func appendLocalControlSocket(_ string, _ http.Handler, instances []*httpInstance) []*httpInstance {
	return instances
}
