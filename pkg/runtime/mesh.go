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
	"math/rand/v2"
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

	healthChecks map[string]HealthChecker

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

	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	// Copy health checks so callers can't mutate after construction.
	checks := make(map[string]HealthChecker, len(o.HealthChecks)+1)
	checks["self"] = HealthCheckerFunc(func(context.Context) HealthResult {
		return HealthResult{Status: StatusHealthy, Output: "ok"}
	})
	for name, checker := range o.HealthChecks {
		checks[name] = checker
	}

	mux := http.NewServeMux()

	return &MeshService{
		opts:         o,
		mux:          mux,
		logger:       logger,
		healthChecks: checks,
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

// AddHealthCheck registers a named health check. Must be called before Start/Run.
func (s *MeshService) AddHealthCheck(name string, checker HealthChecker) {
	s.healthChecks[name] = checker
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
	_, portStr, err := net.SplitHostPort(s.boundAddr)
	if err != nil {
		ln.Close()
		return fmt.Errorf("runtime: parse bound address %s: %w", s.boundAddr, err)
	}
	actualPort, err := strconv.Atoi(portStr)
	if err != nil {
		ln.Close()
		return fmt.Errorf("runtime: parse port %q: %w", portStr, err)
	}

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
			if closeErr := ln.Close(); closeErr != nil {
				s.logger.Warn("close listener", "error", closeErr)
			}
			return fmt.Errorf("runtime: connect to discovery %s: %w", s.opts.DiscoveryAddress, err)
		}
		discoveryClient = pb.NewDiscoveryRegistryClient(grpcConn)
	}

	// Register with Discovery (with retry).
	if s.opts.AutoRegister && discoveryClient != nil {
		regErr := s.retryWithBackoff(ctx, "register", DefaultMaxRetries, func(ctx context.Context) error {
			return s.register(ctx, discoveryClient, actualPort)
		})
		if regErr != nil {
			s.logger.Error("registration failed after retries", "error", regErr)
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
		deregCtx, cancel := context.WithTimeout(context.Background(), DefaultDeregisterTimeout)
		defer cancel()
		s.deregister(deregCtx, discoveryClient)
	}

	// Graceful HTTP shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), DefaultShutdownTimeout)
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

	var consecutiveFailures int

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sendHeartbeatWithRetry(ctx, client); err != nil {
				consecutiveFailures++
				level := slog.LevelWarn
				if consecutiveFailures >= 5 {
					level = slog.LevelError
				}
				s.logger.Log(ctx, level, "heartbeat failed",
					"error", err,
					"consecutiveFailures", consecutiveFailures,
					"serviceId", s.opts.ServiceID,
				)
			} else {
				if consecutiveFailures > 0 {
					s.logger.Info("heartbeat recovered",
						"afterFailures", consecutiveFailures,
						"serviceId", s.opts.ServiceID,
					)
				}
				consecutiveFailures = 0
			}
		}
	}
}

func (s *MeshService) sendHeartbeatWithRetry(ctx context.Context, client pb.DiscoveryRegistryClient) error {
	var lastErr error
	for attempt := range DefaultMaxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(DefaultHeartbeatRetryDelay):
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, DefaultHeartbeatTimeout)
		_, err := client.ReportHealth(reqCtx, &pb.ReportHealthRequest{
			ServiceId: s.opts.ServiceID,
			Status:    pb.HealthStatus_HEALTH_STATUS_HEALTHY,
			Output:    "heartbeat",
		})
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err
		s.logger.Warn("heartbeat attempt failed",
			"attempt", attempt+1,
			"maxAttempts", DefaultMaxRetries,
			"error", err,
		)
	}
	return lastErr
}

type checkResult struct {
	Status   HealthStatus `json:"status"`
	Output   string       `json:"output,omitempty"`
	Duration string       `json:"duration"`
}

func (s *MeshService) healthHandler(w http.ResponseWriter, r *http.Request) {
	overall := StatusHealthy
	checks := make(map[string]checkResult, len(s.healthChecks))

	for name, checker := range s.healthChecks {
		start := time.Now()
		checkCtx, cancel := context.WithTimeout(r.Context(), DefaultHeartbeatTimeout)
		result := checker.Check(checkCtx)
		cancel()

		checks[name] = checkResult{
			Status:   result.Status,
			Output:   result.Output,
			Duration: time.Since(start).String(),
		}

		// Worst status wins: Unhealthy > Degraded > Healthy.
		if result.Status == StatusUnhealthy {
			overall = StatusUnhealthy
		} else if result.Status == StatusDegraded && overall != StatusUnhealthy {
			overall = StatusDegraded
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if overall == StatusUnhealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	resp := map[string]any{
		"status":  overall,
		"service": s.opts.ServiceName,
		"id":      s.opts.ServiceID,
		"checks":  checks,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Warn("health response encode failed", "error", err)
	}
}

// retryWithBackoff retries fn up to maxRetries times with exponential backoff
// and jitter. Returns the last error if all attempts fail.
func (s *MeshService) retryWithBackoff(ctx context.Context, name string, maxRetries int, fn func(ctx context.Context) error) error {
	var lastErr error
	delay := DefaultRetryBaseDelay

	for attempt := range maxRetries {
		if err := fn(ctx); err != nil {
			lastErr = err
			if attempt == maxRetries-1 {
				break
			}
			// Add 0–50% jitter.
			jitter := time.Duration(rand.Int64N(int64(delay) / 2))
			wait := delay + jitter

			s.logger.Warn("retrying "+name,
				"attempt", attempt+1,
				"maxRetries", maxRetries,
				"backoff", wait,
				"error", err,
			)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}

			// Exponential increase, capped.
			delay = min(delay*2, DefaultRetryMaxDelay)
		} else {
			return nil
		}
	}
	return lastErr
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
