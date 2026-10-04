package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// stallingServer is a TrueNAS stand-in that authenticates every connection and,
// while stalled, stops servicing a connection after the next request arrives, the
// way middlewared hangs mid-call. A connection that is not being read does not
// answer pings.
type stallingServer struct {
	server      *httptest.Server
	url         string
	stalled     atomic.Bool
	connections atomic.Int32
}

func newStallingServer(t *testing.T) *stallingServer {
	t.Helper()
	s := &stallingServer{}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	s.url = "ws" + strings.TrimPrefix(s.server.URL, "http")
	t.Cleanup(s.server.Close)
	return s
}

func (s *stallingServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == apiVersionsPath {
		_ = json.NewEncoder(w).Encode([]string{MinAPIVersion})
		return
	}
	s.connections.Add(1)

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	for {
		var req request
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			return
		}
		for s.stalled.Load() {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		resp := response{ID: req.ID, JSONRPC: jsonRPCVersion}
		resp.Result, _ = json.Marshal(true)
		if err := wsjson.Write(r.Context(), conn, resp); err != nil {
			return
		}
	}
}

func newKeepaliveClient(url string, threshold int) *Client {
	return New(Config{
		URL:                  url,
		APIKey:               "key",
		CallTimeout:          testTimeout,
		PingInterval:         50 * time.Millisecond,
		PingTimeout:          50 * time.Millisecond,
		PingFailureThreshold: threshold,
		ReconnectMin:         10 * time.Millisecond,
		ReconnectMax:         50 * time.Millisecond,
	})
}

// stall sends a request that the server sits on for d, so pings go unanswered.
func stall(t *testing.T, srv *stallingServer, c *Client, d time.Duration) {
	t.Helper()
	srv.stalled.Store(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*d)
		defer cancel()
		_ = c.Call(ctx, "pool.query", nil, nil)
	}()
	time.Sleep(d)
	srv.stalled.Store(false)
}

func TestPingLoop_KeepsConnectionThroughShortStall(t *testing.T) {
	srv := newStallingServer(t)
	c := newKeepaliveClient(srv.url, 20)
	defer c.Close()
	assertNoError(t, c.Connect(testContext(t)))

	// Long enough to miss a few pongs, well short of 20 consecutive misses.
	stall(t, srv, c, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assertNoError(t, c.Ping(ctx))
	assertEqual(t, srv.connections.Load(), int32(1))
}

func TestPingLoop_ReconnectsAfterThresholdMisses(t *testing.T) {
	srv := newStallingServer(t)
	c := newKeepaliveClient(srv.url, 1)
	defer c.Close()
	assertNoError(t, c.Connect(testContext(t)))

	stall(t, srv, c, 300*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for srv.connections.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.connections.Load(); got < 2 {
		t.Fatalf("expected the client to reconnect after a missed pong, got %d connection(s)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	assertNoError(t, c.Ping(ctx))
}

func TestFirstReconnectDelay(t *testing.T) {
	c := New(Config{
		URL:             "ws://localhost:1",
		APIKey:          "key",
		ReconnectMin:    time.Second,
		ReconnectMax:    time.Minute,
		ReconnectFactor: 2,
	})
	defer c.Close()

	// Never connected: start at the minimum.
	assertEqual(t, c.firstReconnectDelay(), time.Second)

	// A connection that dropped right after a reconnect continues the backoff.
	c.lastReconnectDelay.Store(int64(8 * time.Second))
	c.connectedAt.Store(time.Now().UnixNano())
	assertEqual(t, c.firstReconnectDelay(), 16*time.Second)

	// ...but never beyond ReconnectMax.
	c.lastReconnectDelay.Store(int64(time.Minute))
	assertEqual(t, c.firstReconnectDelay(), time.Minute)

	// A connection that stayed up for ReconnectMax resets the backoff.
	c.connectedAt.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	assertEqual(t, c.firstReconnectDelay(), time.Second)
}

func TestJitter(t *testing.T) {
	for range 100 {
		got := jitter(10 * time.Second)
		if got < 8*time.Second || got > 12*time.Second {
			t.Fatalf("jitter(10s) = %v, want within 8s..12s", got)
		}
	}
	assertEqual(t, jitter(0), time.Duration(0))
}

func TestGetPool_NotFoundIsDetectable(t *testing.T) {
	mock := NewMockTrueNASServer()
	defer mock.Close()
	mock.SetResponse(methodPoolQuery, MockResponse{Result: []Pool{}})
	c := connectTestClient(t, mock)

	_, err := c.GetPool(testContext(t), "missing")
	assertError(t, err)
	assertTrue(t, IsNotFoundError(err))
}

func TestCall_TimeoutRecordsTransientFailure(t *testing.T) {
	srv := newStallingServer(t)
	c := newKeepaliveClient(srv.url, 100)
	defer c.Close()
	assertNoError(t, c.Connect(testContext(t)))
	assertTrue(t, c.LastTransientFailure().IsZero())

	srv.stalled.Store(true)
	defer srv.stalled.Store(false)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	assertError(t, c.Call(ctx, "pool.query", nil, nil))

	assertTrue(t, c.Connected())
	assertFalse(t, c.LastTransientFailure().Before(start))
}

func TestIsTransientError(t *testing.T) {
	tests := []BoolTestCase{
		{Name: "not connected", Input: ErrNotConnected, Expected: true},
		{Name: "deadline", Input: fmt.Errorf("failed to get dataset: %w", context.DeadlineExceeded), Expected: true},
		{Name: "connection error", Input: &ConnectionError{Op: "read", Err: errors.New("EOF")}, Expected: true},
		{Name: "connection lost", Input: &RPCError{Code: rpcErrCodeConnectionLost, Message: connectionLostMessage}, Expected: true},
		{Name: "TrueNAS error with code -1", Input: &RPCError{Code: -1, Message: "Authentication failed"}, Expected: false},
		{Name: "not found", Input: fmt.Errorf("dataset x: %w", ErrNotFound), Expected: false},
		{Name: "nil", Input: nil, Expected: false},
	}
	runBoolTableTests(t, tests, IsTransientError)
}
