package driver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/truenas/truenas-csi/pkg/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// blackHoleURL returns a ws:// URL whose server accepts TCP connections and then
// never answers, like a TrueNAS whose middlewared is frozen.
func blackHoleURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				<-done
				return
			}
			held = append(held, c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		close(done)
	})
	return "ws://" + ln.Addr().String()
}

// fakeTrueNASURL returns a ws:// URL served by a minimal TrueNAS that accepts
// any API key, knows the pool "tank" and rejects every other method.
func fakeTrueNASURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/versions" {
			_ = json.NewEncoder(w).Encode([]string{client.MinAPIVersion})
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			var req struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID, "jsonrpc": "2.0"}
			switch req.Method {
			case "auth.login_with_api_key":
				resp["result"] = true
			case "core.ping":
				resp["result"] = "pong"
			case "pool.query":
				resp["result"] = []map[string]any{{"id": 1, "name": "tank", "guid": "1"}}
			default:
				resp["error"] = map[string]any{"code": -32601, "message": "unsupported by fake"}
			}
			if err := wsjson.Write(r.Context(), conn, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func newTestDriver(t *testing.T, truenasURL string) (*Driver, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "csi.sock")
	d, err := NewDriver(&DriverConfig{
		NodeID:        "test-node",
		Endpoint:      "unix://" + sock,
		Mode:          DriverModeController,
		TrueNASURL:    truenasURL,
		TrueNASAPIKey: "key",
		DefaultPool:   "tank",
	})
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	return d, sock
}

// runDriver starts d and returns once its socket exists.
func runDriver(t *testing.T, d *Driver, sock string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(GracefulShutdownTimeout + 5*time.Second):
			t.Error("driver did not stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("CSI socket %s not created within 2s", sock)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNewDriver_DoesNotWaitForTrueNAS(t *testing.T) {
	start := time.Now()
	d, _ := newTestDriver(t, blackHoleURL(t))
	defer d.client.Close()

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("NewDriver took %v with an unresponsive TrueNAS; it must not block on the connection", elapsed)
	}
}

func TestProbe_ReadyWhileTrueNASDisconnected(t *testing.T) {
	d, _ := newTestDriver(t, blackHoleURL(t))
	ids := NewIdentityServer(d)

	resp, err := ids.Probe(context.Background(), &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if d.client.Connected() {
		t.Fatal("test precondition: client must be disconnected")
	}
	if !resp.GetReady().GetValue() {
		t.Fatal("Probe reported not ready while TrueNAS is disconnected; the livenessprobe sidecar would fail the container")
	}

	d.client.Close()
	if _, err := ids.Probe(context.Background(), &csi.ProbeRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Probe after shutdown = %v, want FailedPrecondition", err)
	}
}

func TestRun_ServesBeforeTrueNASResponds(t *testing.T) {
	d, sock := newTestDriver(t, blackHoleURL(t))
	runDriver(t, d, sock)

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial driver: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := csi.NewIdentityClient(conn).Probe(ctx, &csi.ProbeRequest{})
	if err != nil {
		t.Fatalf("Probe over gRPC: %v", err)
	}
	if !resp.GetReady().GetValue() {
		t.Fatal("Probe over gRPC reported not ready while TrueNAS is unresponsive")
	}
	if d.BackendReady() {
		t.Fatal("backend reported ready although TrueNAS never answered")
	}
}

func TestRun_BackendReadyOnceTrueNASAnswers(t *testing.T) {
	d, sock := newTestDriver(t, fakeTrueNASURL(t))
	runDriver(t, d, sock)

	deadline := time.Now().Add(5 * time.Second)
	for !d.BackendReady() {
		if time.Now().After(deadline) {
			t.Fatal("backend did not become ready against a healthy TrueNAS")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInterceptor_TrueNASCallsUnavailableWhileDisconnected(t *testing.T) {
	d, _ := newTestDriver(t, blackHoleURL(t))
	defer d.client.Close()

	tests := []struct {
		name   string
		method string
		req    any
		call   grpc.UnaryHandler
	}{
		{
			name:   "CreateVolume",
			method: "/csi.v1.Controller/CreateVolume",
			req: &csi.CreateVolumeRequest{
				Name: "pvc-1",
				VolumeCapabilities: []*csi.VolumeCapability{{
					AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
					AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
				}},
			},
			call: func(ctx context.Context, req any) (any, error) {
				return d.controllerServer.CreateVolume(ctx, req.(*csi.CreateVolumeRequest))
			},
		},
		{
			name:   "CreateVolume with pool parameter",
			method: "/csi.v1.Controller/CreateVolume",
			req: &csi.CreateVolumeRequest{
				Name:       "pvc-2",
				Parameters: map[string]string{"pool": "tank"},
				VolumeCapabilities: []*csi.VolumeCapability{{
					AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
					AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
				}},
			},
			call: func(ctx context.Context, req any) (any, error) {
				return d.controllerServer.CreateVolume(ctx, req.(*csi.CreateVolumeRequest))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, err := d.unaryInterceptor(ctx, tt.req, &grpc.UnaryServerInfo{FullMethod: tt.method}, tt.call)
			if status.Code(err) != codes.Unavailable {
				t.Fatalf("%s while TrueNAS is disconnected = %v, want Unavailable", tt.method, err)
			}
		})
	}
}

func TestUnavailableIfDisconnected(t *testing.T) {
	disconnected, _ := newTestDriver(t, blackHoleURL(t))
	defer disconnected.client.Close()

	connected, _ := newTestDriver(t, fakeTrueNASURL(t))
	defer connected.client.Close()
	if err := connected.client.Connect(context.Background()); err != nil {
		t.Fatalf("connect to fake TrueNAS: %v", err)
	}

	internal := status.Error(codes.Internal, "boom")
	tests := []struct {
		name string
		d    *Driver
		in   error
		want codes.Code
	}{
		{"internal while disconnected", disconnected, internal, codes.Unavailable},
		{"plain error while disconnected", disconnected, errors.New("boom"), codes.Unavailable},
		{"invalid argument while disconnected", disconnected, status.Error(codes.InvalidArgument, "bad"), codes.InvalidArgument},
		{"not found while disconnected", disconnected, status.Error(codes.NotFound, "gone"), codes.NotFound},
		{"internal while connected", connected, internal, codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.d.unavailableIfDisconnected(tt.in, tt.d.client.Connected())
			if status.Code(got) != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
