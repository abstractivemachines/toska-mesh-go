package runtime

import (
	"context"
	"encoding/json"
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

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "Healthy" {
		t.Fatalf("expected status=Healthy, got %q", body["status"])
	}
	if body["service"] != "health-test" {
		t.Fatalf("expected service=health-test, got %q", body["service"])
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
