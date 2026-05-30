package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func TestProxy_ForwardsHTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "backend")
	}))
	defer backend.Close()

	manager := NewManager()
	defer manager.CloseAll()

	proxyURL, err := manager.StartOrUpdate("backend", freePort(t), backend.URL)
	require.NoError(t, err)

	resp, err := http.Get(proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "backend", string(body))
}

func TestProxy_UpdatesTarget(t *testing.T) {
	firstBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
	}))
	defer firstBackend.Close()

	secondBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "second")
	}))
	defer secondBackend.Close()

	manager := NewManager()
	defer manager.CloseAll()

	port := freePort(t)
	proxyURL, err := manager.StartOrUpdate("backend", port, firstBackend.URL)
	require.NoError(t, err)

	proxyURL, err = manager.StartOrUpdate("backend", port, secondBackend.URL)
	require.NoError(t, err)

	resp, err := http.Get(proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "second", string(body))
}

func TestProxy_ReturnsServiceUnavailableWithoutTarget(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()

	proxyURL, err := manager.StartOrUpdate("backend", freePort(t), "")
	require.NoError(t, err)

	resp, err := http.Get(proxyURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestProxy_ForwardsWebSocketUpgrade(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			t.Fatalf("expected websocket upgrade, got %q", r.Header.Get("Upgrade"))
		}

		hijacker, ok := w.(http.Hijacker)
		require.True(t, ok)

		conn, rw, err := hijacker.Hijack()
		require.NoError(t, err)
		defer conn.Close()

		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		_, _ = rw.WriteString("Connection: Upgrade\r\n")
		_, _ = rw.WriteString("Upgrade: websocket\r\n\r\n")
		require.NoError(t, rw.Flush())
	}))
	defer backend.Close()

	manager := NewManager()
	defer manager.CloseAll()

	port := freePort(t)
	_, err := manager.StartOrUpdate("backend", port, backend.URL)
	require.NoError(t, err)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("GET /ws HTTP/1.1\r\n" +
		"Host: 127.0.0.1\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"))
	require.NoError(t, err)

	status, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, status, "101 Switching Protocols")
}
