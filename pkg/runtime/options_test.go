package runtime

import (
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	o := DefaultOptions()

	if o.ServiceName != "mesh-service" {
		t.Fatalf("expected ServiceName=mesh-service, got %q", o.ServiceName)
	}
	if o.Address != "0.0.0.0" {
		t.Fatalf("expected Address=0.0.0.0, got %q", o.Address)
	}
	if o.Port != 8080 {
		t.Fatalf("expected Port=8080, got %d", o.Port)
	}
	if o.HealthEndpoint != "/health" {
		t.Fatalf("expected HealthEndpoint=/health, got %q", o.HealthEndpoint)
	}
	if o.HealthInterval != 30*time.Second {
		t.Fatalf("expected HealthInterval=30s, got %v", o.HealthInterval)
	}
	if !o.HeartbeatEnabled {
		t.Fatal("expected HeartbeatEnabled=true")
	}
	if !o.AutoRegister {
		t.Fatal("expected AutoRegister=true")
	}
	if o.Routing.Scheme != "http" {
		t.Fatalf("expected Routing.Scheme=http, got %q", o.Routing.Scheme)
	}
	if o.Routing.Strategy != RoundRobin {
		t.Fatalf("expected Routing.Strategy=RoundRobin, got %q", o.Routing.Strategy)
	}
	if o.Routing.Weight != 1 {
		t.Fatalf("expected Routing.Weight=1, got %d", o.Routing.Weight)
	}
}

func TestFunctionalOptions(t *testing.T) {
	o := DefaultOptions()
	opts := []Option{
		WithServiceName("test-svc"),
		WithServiceID("svc-42"),
		WithPort(9090),
		WithAddress("127.0.0.1"),
		WithAdvertisedAddress("10.0.0.5"),
		WithHealthEndpoint("/ready"),
		WithHealthInterval(15 * time.Second),
		WithHeartbeat(false),
		WithAutoRegister(false),
		WithDiscoveryAddress("discovery:8080"),
		WithMetadata("env", "staging"),
		WithRoutingStrategy(LeastConnections),
		WithRoutingWeight(5),
		WithRoutingScheme("https"),
	}

	for _, fn := range opts {
		fn(&o)
	}

	if o.ServiceName != "test-svc" {
		t.Fatalf("ServiceName: got %q", o.ServiceName)
	}
	if o.ServiceID != "svc-42" {
		t.Fatalf("ServiceID: got %q", o.ServiceID)
	}
	if o.Port != 9090 {
		t.Fatalf("Port: got %d", o.Port)
	}
	if o.Address != "127.0.0.1" {
		t.Fatalf("Address: got %q", o.Address)
	}
	if o.AdvertisedAddress != "10.0.0.5" {
		t.Fatalf("AdvertisedAddress: got %q", o.AdvertisedAddress)
	}
	if o.HealthEndpoint != "/ready" {
		t.Fatalf("HealthEndpoint: got %q", o.HealthEndpoint)
	}
	if o.HealthInterval != 15*time.Second {
		t.Fatalf("HealthInterval: got %v", o.HealthInterval)
	}
	if o.HeartbeatEnabled {
		t.Fatal("expected HeartbeatEnabled=false")
	}
	if o.AutoRegister {
		t.Fatal("expected AutoRegister=false")
	}
	if o.DiscoveryAddress != "discovery:8080" {
		t.Fatalf("DiscoveryAddress: got %q", o.DiscoveryAddress)
	}
	if o.Metadata["env"] != "staging" {
		t.Fatalf("Metadata[env]: got %q", o.Metadata["env"])
	}
	if o.Routing.Strategy != LeastConnections {
		t.Fatalf("Strategy: got %q", o.Routing.Strategy)
	}
	if o.Routing.Weight != 5 {
		t.Fatalf("Weight: got %d", o.Routing.Weight)
	}
	if o.Routing.Scheme != "https" {
		t.Fatalf("Scheme: got %q", o.Routing.Scheme)
	}
}
