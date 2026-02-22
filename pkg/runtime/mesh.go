// Package runtime provides the Go runtime SDK for services joining the ToskaMesh
// service mesh. It handles auto-registration with Discovery, health endpoints,
// heartbeat/TTL renewal, and graceful deregistration on shutdown.
//
// Usage:
//
//	svc, err := runtime.New(
//	    runtime.WithServiceName("my-service"),
//	    runtime.WithPort(9090),
//	)
//	if err != nil { log.Fatal(err) }
//
//	svc.Handle("GET /hello", helloHandler)
//	if err := svc.Run(ctx); err != nil { log.Fatal(err) }
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	pb "github.com/toska-mesh/toska-mesh-go/pkg/meshpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// MeshService is a mesh-aware HTTP service that auto-registers with Discovery,
// sends heartbeats, and deregisters on shutdown.
type MeshService struct {
	opts   ServiceOptions
	mux    *http.ServeMux
	logger *slog.Logger

	// Set after Start; used by tests.
	boundAddr string
	mu        sync.Mutex
}

// New creates a MeshService with the given functional options.
func New(opts ...Option) (*MeshService, error) {
	o := DefaultOptions()
	for _, fn := range opts {
		fn(&o)
	}

	if o.ServiceName == "" {
		return nil, fmt.Errorf("runtime: ServiceName is required")
	}

	if o.ServiceID == "" {
		o.ServiceID = fmt.Sprintf("%s-%d", o.ServiceName, time.Now().UnixNano())
	}

	if o.AdvertisedAddress == "" {
		o.AdvertisedAddress = o.Address
	}

	if o.Routing.HealthCheckEndpoint == "" {
		o.Routing.HealthCheckEndpoint = o.HealthEndpoint
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	mux := http.NewServeMux()

	return &MeshService{
		opts:   o,
		mux:    mux,
		logger: logger,
	}, nil
}

// Handle registers an HTTP handler on the service's mux.
// Pattern follows Go 1.22+ enhanced ServeMux syntax (e.g. "GET /hello").
func (s *MeshService) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers an HTTP handler function on the service's mux.
func (s *MeshService) HandleFunc(pattern string, handler http.HandlerFunc) {
	s.mux.HandleFunc(pattern, handler)
}

// Addr returns the bound address after Start. Empty before Start.
func (s *MeshService) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundAddr
}

// Run starts the service, registers with Discovery, runs the heartbeat loop,
// and blocks until ctx is cancelled or a SIGINT/SIGTERM is received.
// On shutdown it deregisters from Discovery.
func (s *MeshService) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return s.start(ctx)
}

// Start is like Run but does not install signal handlers. Useful for testing
// and embedding. The caller must cancel ctx to trigger shutdown.
func (s *MeshService) Start(ctx context.Context) error {
	return s.start(ctx)
}

func (s *MeshService) start(ctx context.Context) error {
	// Register the health endpoint.
	s.mux.HandleFunc("GET "+s.opts.HealthEndpoint, s.healthHandler)

	// Bind listener.
	addr := net.JoinHostPort(s.opts.Address, strconv.Itoa(s.opts.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("runtime: listen %s: %w", addr, err)
	}

	s.mu.Lock()
	s.boundAddr = ln.Addr().String()
	s.mu.Unlock()

	// Resolve actual port if ephemeral.
	_, portStr, _ := net.SplitHostPort(s.boundAddr)
	actualPort, _ := strconv.Atoi(portStr)

	s.logger.Info("service starting",
		"service", s.opts.ServiceName,
		"id", s.opts.ServiceID,
		"addr", s.boundAddr,
	)

	// gRPC connection to Discovery.
	var discoveryClient pb.DiscoveryRegistryClient
	var grpcConn *grpc.ClientConn
	if s.opts.AutoRegister || s.opts.HeartbeatEnabled {
		grpcConn, err = grpc.NewClient(
			s.opts.DiscoveryAddress,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			ln.Close()
			return fmt.Errorf("runtime: connect to discovery %s: %w", s.opts.DiscoveryAddress, err)
		}
		discoveryClient = pb.NewDiscoveryRegistryClient(grpcConn)
	}

	// Register with Discovery.
	if s.opts.AutoRegister && discoveryClient != nil {
		if regErr := s.register(ctx, discoveryClient, actualPort); regErr != nil {
			s.logger.Error("registration failed", "error", regErr)
			// Continue running — service may work without registration.
		}
	}

	// Start heartbeat goroutine.
	heartbeatDone := make(chan struct{})
	if s.opts.HeartbeatEnabled && discoveryClient != nil {
		go func() {
			defer close(heartbeatDone)
			s.heartbeatLoop(ctx, discoveryClient)
		}()
	} else {
		close(heartbeatDone)
	}

	// Start HTTP server.
	server := &http.Server{Handler: s.mux}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(ln); err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	// Wait for shutdown signal.
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	s.logger.Info("shutting down", "service", s.opts.ServiceName)

	// Deregister from Discovery.
	if s.opts.AutoRegister && discoveryClient != nil {
		deregCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.deregister(deregCtx, discoveryClient)
	}

	// Graceful HTTP shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)

	// Wait for heartbeat to stop.
	<-heartbeatDone

	// Close gRPC connection.
	if grpcConn != nil {
		grpcConn.Close()
	}

	s.logger.Info("stopped", "service", s.opts.ServiceName)
	return nil
}

func (s *MeshService) register(ctx context.Context, client pb.DiscoveryRegistryClient, actualPort int) error {
	metadata := s.buildMetadata()

	req := &pb.RegisterServiceRequest{
		ServiceName: s.opts.ServiceName,
		ServiceId:   s.opts.ServiceID,
		Address:     s.opts.AdvertisedAddress,
		Port:        int32(actualPort),
		Metadata:    metadata,
		HealthCheck: &pb.HealthCheckConfig{
			Endpoint:           s.opts.HealthEndpoint,
			IntervalSeconds:    int32(s.opts.HealthInterval.Seconds()),
			TimeoutSeconds:     int32(s.opts.HealthTimeout.Seconds()),
			UnhealthyThreshold: int32(s.opts.UnhealthyThreshold),
		},
	}

	resp, err := client.Register(ctx, req)
	if err != nil {
		return fmt.Errorf("gRPC Register: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("registration rejected: %s", resp.ErrorMessage)
	}

	s.logger.Info("registered with discovery",
		"serviceId", resp.ServiceId,
		"discovery", s.opts.DiscoveryAddress,
	)
	return nil
}

func (s *MeshService) deregister(ctx context.Context, client pb.DiscoveryRegistryClient) {
	// Report degraded status first (like C# SDK).
	_, _ = client.ReportHealth(ctx, &pb.ReportHealthRequest{
		ServiceId: s.opts.ServiceID,
		Status:    pb.HealthStatus_HEALTH_STATUS_DEGRADED,
		Output:    "shutting down",
	})

	resp, err := client.Deregister(ctx, &pb.DeregisterServiceRequest{
		ServiceId: s.opts.ServiceID,
	})
	if err != nil {
		s.logger.Error("deregistration failed", "error", err)
		return
	}
	if resp.Removed {
		s.logger.Info("deregistered from discovery", "serviceId", s.opts.ServiceID)
	}
}

func (s *MeshService) heartbeatLoop(ctx context.Context, client pb.DiscoveryRegistryClient) {
	ticker := time.NewTicker(s.opts.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sendHeartbeat(ctx, client)
		}
	}
}

func (s *MeshService) sendHeartbeat(ctx context.Context, client pb.DiscoveryRegistryClient) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := client.ReportHealth(reqCtx, &pb.ReportHealthRequest{
		ServiceId: s.opts.ServiceID,
		Status:    pb.HealthStatus_HEALTH_STATUS_HEALTHY,
		Output:    "heartbeat",
	})
	if err != nil {
		s.logger.Warn("heartbeat failed", "error", err, "serviceId", s.opts.ServiceID)
	}
}

func (s *MeshService) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "Healthy",
		"service": s.opts.ServiceName,
		"id":      s.opts.ServiceID,
	})
}

func (s *MeshService) buildMetadata() map[string]string {
	m := make(map[string]string, len(s.opts.Metadata)+4)
	for k, v := range s.opts.Metadata {
		m[k] = v
	}
	m["scheme"] = s.opts.Routing.Scheme
	m["health_check_endpoint"] = s.opts.Routing.HealthCheckEndpoint
	m["lb_strategy"] = string(s.opts.Routing.Strategy)
	if s.opts.Routing.Weight > 0 {
		m["weight"] = strconv.Itoa(s.opts.Routing.Weight)
	}
	return m
}
