package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

const Host = "127.0.0.1"

type Instance struct {
	port   int
	url    string
	server *http.Server
	target atomic.Value
}

func newInstance(port int, target string) (*Instance, error) {
	instance := &Instance{
		port: port,
		url:  fmt.Sprintf("http://%s:%d", Host, port),
	}
	instance.target.Store(target)

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", Host, port))
	if err != nil {
		return nil, err
	}

	instance.server = &http.Server{Handler: instance}

	go func() {
		err := instance.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// There is no logger in this package. The next request/start attempt
			// will surface failures through the caller-visible APIs.
		}
	}()

	return instance, nil
}

func (i *Instance) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, _ := i.target.Load().(string)
	if target == "" {
		http.Error(w, "proxy target unavailable", http.StatusServiceUnavailable)
		return
	}

	targetURL, err := url.Parse(target)
	if err != nil {
		http.Error(w, "proxy target invalid", http.StatusServiceUnavailable)
		return
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "proxy target unavailable", http.StatusServiceUnavailable)
	}
	reverseProxy.ServeHTTP(w, r)
}

func (i *Instance) URL() string {
	return i.url
}

func (i *Instance) Port() int {
	return i.port
}

func (i *Instance) SetTarget(target string) {
	i.target.Store(target)
}

func (i *Instance) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return i.server.Shutdown(ctx)
}

type Manager struct {
	mu        sync.Mutex
	instances map[string]*Instance
}

func NewManager() *Manager {
	return &Manager{
		instances: make(map[string]*Instance),
	}
}

func (m *Manager) StartOrUpdate(id string, port int, target string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		return "", fmt.Errorf("proxy id is required")
	}

	if existing := m.instances[id]; existing != nil {
		if existing.Port() == port {
			existing.SetTarget(target)
			return existing.URL(), nil
		}

		_ = existing.Close()
		delete(m.instances, id)
	}

	instance, err := newInstance(port, target)
	if err != nil {
		return "", err
	}

	m.instances[id] = instance
	return instance.URL(), nil
}

func (m *Manager) UpdateTarget(id string, target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance := m.instances[id]
	if instance == nil {
		return false
	}

	instance.SetTarget(target)
	return true
}

func (m *Manager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	instance := m.instances[id]
	if instance == nil {
		return
	}

	_ = instance.Close()
	delete(m.instances, id)
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, instance := range m.instances {
		_ = instance.Close()
		delete(m.instances, id)
	}
}
