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
	"sync"
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
// any API key, knows the pool "tank" and rejects every other method. behavior
// overrides individual methods: "stall" does not answer until release is
// called, "missing" answers with a does-not-exist error.
func fakeTrueNASURL(t *testing.T, behavior map[string]string) (url string, release func()) {
	t.Helper()
	stalled := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(stalled) }) }
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
			switch {
			case behavior[req.Method] == "stall":
				<-stalled
				return
			case behavior[req.Method] == "missing":
				resp["error"] = map[string]any{"code": -32001, "message": "does not exist"}
			case behavior[req.Method] == "empty":
				resp["result"] = []any{}
			case req.Method == "auth.login_with_api_key":
				resp["result"] = true
			case req.Method == "core.ping":
				resp["result"] = "pong"
			case req.Method == "pool.query":
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
	t.Cleanup(release)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), release
}

// connectedTestDriver returns a driver whose client is connected to a fake TrueNAS.
func connectedTestDriver(t *testing.T, behavior map[string]string) *Driver {
	t.Helper()
	url, release := fakeTrueNASURL(t, behavior)
	d, _ := newTestDriver(t, url)
	// Closing waits for the websocket close handshake, which a stalled server
	// never completes.
	t.Cleanup(func() {
		release()
		d.client.Close()
	})
	if err := d.client.Connect(context.Background()); err != nil {
		t.Fatalf("connect to fake TrueNAS: %v", err)
	}
	return d
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
	url, _ := fakeTrueNASURL(t, nil)
	d, sock := newTestDriver(t, url)
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

func TestUnavailableIfTrueNASUnreachable(t *testing.T) {
	disconnected, _ := newTestDriver(t, blackHoleURL(t))
	defer disconnected.client.Close()
	connected := connectedTestDriver(t, nil)

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
			got := tt.d.unavailableIfTrueNASUnreachable(tt.in, tt.d.client.Connected(), time.Now())
			if status.Code(got) != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// During a stall the connection stays up, so a call that timed out talking to
// TrueNAS must still be reported as retryable.
func TestUnavailableIfTrueNASUnreachable_StallWhileConnected(t *testing.T) {
	d := connectedTestDriver(t, map[string]string{"pool.dataset.query": "stall"})

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = d.client.Call(ctx, "pool.dataset.query", nil, nil)

	if !d.client.Connected() {
		t.Fatal("test precondition: client must still be connected")
	}
	got := d.unavailableIfTrueNASUnreachable(status.Error(codes.Internal, "boom"), true, start)
	if status.Code(got) != codes.Unavailable {
		t.Fatalf("got %v, want Unavailable", got)
	}
}

func TestControllerPublishVolume_LookupErrors(t *testing.T) {
	req := &csi.ControllerPublishVolumeRequest{
		VolumeId: "tank/pvc-1",
		NodeId:   "node-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
		},
	}

	tests := []struct {
		name     string
		behavior string
		want     codes.Code
	}{
		// The attacher treats NotFound as final; a stall must not look like a missing volume.
		{"TrueNAS stalls", "stall", codes.Unavailable},
		{"volume missing", "missing", codes.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := connectedTestDriver(t, map[string]string{"pool.dataset.get_instance": tt.behavior})
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			_, err := d.unaryInterceptor(ctx, req, &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/ControllerPublishVolume"},
				func(ctx context.Context, req any) (any, error) {
					return d.controllerServer.ControllerPublishVolume(ctx, req.(*csi.ControllerPublishVolumeRequest))
				})
			if status.Code(err) != tt.want {
				t.Fatalf("ControllerPublishVolume = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListSnapshots_LookupErrors(t *testing.T) {
	const (
		datasetGet    = "pool.dataset.get_instance"
		snapshotQuery = "pool.snapshot.query"
	)
	tests := []struct {
		name     string
		req      *csi.ListSnapshotsRequest
		behavior map[string]string
		want     codes.Code
	}{
		// An empty list tells the snapshot controller the snapshots are gone,
		// so a stalled TrueNAS must produce an error instead.
		{
			"by snapshot ID, query stalls", &csi.ListSnapshotsRequest{SnapshotId: "tank/pvc-1@snap-1"},
			map[string]string{snapshotQuery: "stall"},
			codes.Unavailable,
		},
		{
			"by source volume, lookup stalls", &csi.ListSnapshotsRequest{SourceVolumeId: "tank/pvc-1"},
			map[string]string{datasetGet: "stall"},
			codes.Unavailable,
		},
		{
			"by source volume, query stalls", &csi.ListSnapshotsRequest{SourceVolumeId: "tank/pvc-1"},
			map[string]string{datasetGet: "missing", snapshotQuery: "stall"},
			codes.Unavailable,
		},
		{
			"all snapshots, query stalls", &csi.ListSnapshotsRequest{},
			map[string]string{snapshotQuery: "stall"},
			codes.Unavailable,
		},
		{
			"by source volume, volume missing", &csi.ListSnapshotsRequest{SourceVolumeId: "tank/pvc-1"},
			map[string]string{datasetGet: "missing", snapshotQuery: "empty"},
			codes.OK,
		},
		{
			"by snapshot ID, no snapshots", &csi.ListSnapshotsRequest{SnapshotId: "tank/pvc-1@snap-1"},
			map[string]string{snapshotQuery: "empty"},
			codes.OK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := connectedTestDriver(t, tt.behavior)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			resp, err := d.unaryInterceptor(ctx, tt.req, &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/ListSnapshots"},
				func(ctx context.Context, req any) (any, error) {
					return d.controllerServer.ListSnapshots(ctx, req.(*csi.ListSnapshotsRequest))
				})
			if status.Code(err) != tt.want {
				t.Fatalf("ListSnapshots = %v, want %v", err, tt.want)
			}
			if tt.want == codes.OK {
				if entries := resp.(*csi.ListSnapshotsResponse).Entries; len(entries) != 0 {
					t.Fatalf("ListSnapshots returned %d entries, want none", len(entries))
				}
			}
		})
	}
}
