package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// IdentityServer implements the CSI Identity service.
type IdentityServer struct {
	driver *Driver
	csi.UnimplementedIdentityServer
}

// NewIdentityServer creates a new CSI identity service.
func NewIdentityServer(driver *Driver) *IdentityServer {
	return &IdentityServer{
		driver: driver,
	}
}

// GetPluginInfo returns the driver name and version.
func (s *IdentityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("GetPluginInfo called")

	if s.driver.name == "" {
		return nil, status.Error(codes.Unavailable, "driver name not configured")
	}

	if s.driver.version == "" {
		return nil, status.Error(codes.Unavailable, "driver version not configured")
	}

	return &csi.GetPluginInfoResponse{
		Name:          s.driver.name,
		VendorVersion: s.driver.version,
	}, nil
}

// GetPluginCapabilities returns the driver's capabilities.
func (s *IdentityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("GetPluginCapabilities called")

	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: s.driver.pluginCaps,
	}, nil
}

// Probe reports whether the plugin is serving. It reports ready whenever the
// gRPC server answers, regardless of TrueNAS connectivity: the livenessprobe
// sidecar turns Ready=false into a failed liveness check, and restarting the
// plugin cannot make an unresponsive TrueNAS API respond. TrueNAS-backed calls
// return Unavailable while the API is unreachable, and connection changes are
// logged by the client.
func (s *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("Probe called",
		"truenasConnected", s.driver.client.Connected(), "backendReady", s.driver.BackendReady())

	if s.driver.client.Closed() {
		return nil, status.Error(codes.FailedPrecondition, "driver is shutting down")
	}

	return &csi.ProbeResponse{
		Ready: &wrapperspb.BoolValue{Value: true},
	}, nil
}
