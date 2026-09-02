package control

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type httpInstance struct {
	name  string
	srv   *http.Server
	serve func() error
}

func newHTTPInstance(name, addr string, handler http.Handler) *httpInstance {
	srv := &http.Server{Addr: addr, Handler: handler}
	return &httpInstance{
		name: name,
		srv:  srv,
		serve: func() error {
			return srv.ListenAndServe()
		},
	}
}

func newTLSInstance(name, addr string, cert tls.Certificate, handler http.Handler) (*httpInstance, error) {
	srv := &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: tlsServerConfig(cert, clusterCADir()),
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &httpInstance{
		name: name,
		srv:  srv,
		serve: func() error {
			return srv.ServeTLS(ln, "", "")
		},
	}, nil
}

// serveHTTPServers listens until ctx is cancelled (SIGTERM/SIGINT) or a
// server fails. Shutdown is always attempted so sockets close before
// the writer lease is released.
func serveHTTPServers(ctx context.Context, instances []*httpInstance) error {
	errc := make(chan error, len(instances))
	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		go func(inst *httpInstance) {
			defer wg.Done()
			log.Printf("ndl-control listening on %s %s", inst.name, inst.srv.Addr)
			err := inst.serve()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}(inst)
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, inst := range instances {
		_ = inst.srv.Shutdown(shutCtx)
	}
	wg.Wait()
	return serveErr
}
