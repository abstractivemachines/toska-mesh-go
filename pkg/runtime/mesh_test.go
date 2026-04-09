package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestNew_RequiresServiceName(t *testing.T) {
	_, err := New(WithServiceName(""))
	if err == nil {
		t.Fatal("expected error for empty service name")
	}
}

func TestNew_GeneratesServiceID(t *testing.T) {
	svc, err := New(WithServiceName("test"))
	if err != nil {
		t.Fatal(err)
	}
	if svc.opts.ServiceID == "" {
		t.Fatal("expected auto-generated service ID")
	}
}

func TestNew_SetsAdvertisedAddressDefault(t *testing.T) {
	svc, err := New(WithServiceName("test"), WithAddress("10.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	if svc.opts.AdvertisedAddress != "10.0.0.1" {
		t.Fatalf("expected AdvertisedAddress=10.0.0.1, got %q", svc.opts.AdvertisedAddress)
	}
}

func TestNew_SetsRoutingHealthCheckEndpoint(t *testing.T) {
	svc, err := New(WithServiceName("test"), WithHealthEndpoint("/ready"))
	if err != nil {
		t.Fatal(err)
	}
	if svc.opts.Routing.HealthCheckEndpoint != "/ready" {
		t.Fatalf("expected Routing.HealthCheckEndpoint=/ready, got %q", svc.opts.Routing.HealthCheckEndpoint)
	}
}

func TestMeshService_HealthEndpoint(t *testing.T) {
	svc, err := New(
		WithServiceName("health-test"),
		WithPort(0), // ephemeral
		WithAutoRegister(false),
		WithHeartbeat(false),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(ctx)
	}()

	// Wait for the server to bind.
	var addr string
	for range 50 {
		addr = svc.Addr()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("service did not bind")
	}

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body struct {
		Status  string                    `json:"status"`
		Service string                    `json:"service"`
		ID      string                    `json:"id"`
		Checks  map[string]map[string]any `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "Healthy" {
		t.Fatalf("expected status=Healthy, got %q", body.Status)
	}
	if body.Service != "health-test" {
		t.Fatalf("expected service=health-test, got %q", body.Service)
	}
	if _, ok := body.Checks["self"]; !ok {
		t.Fatal("expected 'self' check in response")
	}

	cancel()
	<-done
}

func TestMeshService_CustomHandler(t *testing.T) {
	svc, err := New(
		WithServiceName("handler-test"),
		WithPort(0),
		WithAutoRegister(false),
		WithHeartbeat(false),
	)
	if err != nil {
		t.Fatal(err)
	}

	svc.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello mesh"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(ctx)
	}()

	var addr string
	for range 50 {
		addr = svc.Addr()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("service did not bind")
	}

	resp, err := http.Get("http://" + addr + "/hello")
	if err != nil {
		t.Fatalf("GET /hello: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello mesh" {
		t.Fatalf("expected 'hello mesh', got %q", string(body))
	}

	cancel()
	<-done
}

func TestMeshService_EphemeralPort(t *testing.T) {
	svc, err := New(
		WithServiceName("ephemeral-test"),
		WithPort(0),
		WithAutoRegister(false),
		WithHeartbeat(false),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Start(ctx)
	}()

	var addr string
	for range 50 {
		addr = svc.Addr()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("service did not bind")
	}

	// Port should not be 0 anymore.
	if addr == "0.0.0.0:0" {
		t.Fatal("expected ephemeral port to be resolved, still 0")
	}

	cancel()
	<-done
}

func TestHealthCheck_CustomChecks(t *testing.T) {
	svc, err := New(
		WithServiceName("custom-health"),
		WithPort(0),
		WithAutoRegister(false),
		WithHeartbeat(false),
		WithHealthCheck("db", HealthCheckerFunc(func(context.Context) HealthResult {
			return HealthResult{Status: StatusHealthy, Output: "connected"}
		})),
		WithHealthCheck("cache", HealthCheckerFunc(func(context.Context) HealthResult {
			return HealthResult{Status: StatusUnhealthy, Output: "connection refused"}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Start(ctx) }()

	var addr string
	for range 50 {
		addr = svc.Addr()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("service did not bind")
	}

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for unhealthy check, got %d", resp.StatusCode)
	}

	var body struct {
		Status string                    `json:"status"`
		Checks map[string]map[string]any `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != string(StatusUnhealthy) {
		t.Fatalf("expected overall Unhealthy, got %q", body.Status)
	}
	if body.Checks["db"]["status"] != string(StatusHealthy) {
		t.Fatalf("expected db=Healthy, got %v", body.Checks["db"]["status"])
	}
	if body.Checks["cache"]["status"] != string(StatusUnhealthy) {
		t.Fatalf("expected cache=Unhealthy, got %v", body.Checks["cache"]["status"])
	}
	if _, ok := body.Checks["self"]; !ok {
		t.Fatal("expected 'self' check to always be present")
	}

	cancel()
	<-done
}

func TestHealthCheck_DegradedReturns200(t *testing.T) {
	svc, err := New(
		WithServiceName("degraded-health"),
		WithPort(0),
		WithAutoRegister(false),
		WithHeartbeat(false),
		WithHealthCheck("slow-dep", HealthCheckerFunc(func(context.Context) HealthResult {
			return HealthResult{Status: StatusDegraded, Output: "high latency"}
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- svc.Start(ctx) }()

	var addr string
	for range 50 {
		addr = svc.Addr()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("service did not bind")
	}

	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	// Degraded still returns 200.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for degraded, got %d", resp.StatusCode)
	}

	var body struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Status != string(StatusDegraded) {
		t.Fatalf("expected Degraded, got %q", body.Status)
	}

	cancel()
	<-done
}

func TestRetryWithBackoff(t *testing.T) {
	svc, err := New(WithServiceName("retry-test"))
	if err != nil {
		t.Fatal(err)
	}

	// Fail twice then succeed.
	attempts := 0
	retryErr := svc.retryWithBackoff(context.Background(), "test-op", 3, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("transient error %d", attempts)
		}
		return nil
	})

	if retryErr != nil {
		t.Fatalf("expected success after retries, got %v", retryErr)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryWithBackoff_AllFail(t *testing.T) {
	svc, err := New(WithServiceName("retry-fail-test"))
	if err != nil {
		t.Fatal(err)
	}

	attempts := 0
	retryErr := svc.retryWithBackoff(context.Background(), "test-op", 3, func(context.Context) error {
		attempts++
		return fmt.Errorf("permanent error")
	})

	if retryErr == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestBuildMetadata(t *testing.T) {
	svc, err := New(
		WithServiceName("meta-test"),
		WithMetadata("env", "prod"),
		WithMetadata("version", "1.2.3"),
		WithRoutingStrategy(WeightedRoundRobin),
		WithRoutingWeight(3),
		WithRoutingScheme("https"),
	)
	if err != nil {
		t.Fatal(err)
	}

	m := svc.buildMetadata()

	checks := map[string]string{
		"env":                    "prod",
		"version":                "1.2.3",
		"scheme":                 "https",
		"health_check_endpoint":  "/health",
		"lb_strategy":            "WeightedRoundRobin",
		"weight":                 "3",
	}

	for k, want := range checks {
		if got := m[k]; got != want {
			t.Errorf("metadata[%q] = %q, want %q", k, got, want)
		}
	}
}
