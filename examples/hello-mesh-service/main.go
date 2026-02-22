// hello-mesh-service is a minimal example of a Go service joining the ToskaMesh mesh.
//
// Run:
//
//	go run ./examples/hello-mesh-service
//
// Environment variables:
//
//	MESH_SERVICE_NAME       Service name (default: hello-go)
//	MESH_SERVICE_PORT       HTTP port (default: 9090)
//	MESH_DISCOVERY_ADDRESS  Discovery gRPC address (default: localhost:8080)
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/toska-mesh/toska-mesh-go/pkg/runtime"
)

func main() {
	name := envOr("MESH_SERVICE_NAME", "hello-go")
	port := envInt("MESH_SERVICE_PORT", 9090)
	discovery := envOr("MESH_DISCOVERY_ADDRESS", "localhost:8080")

	svc, err := runtime.New(
		runtime.WithServiceName(name),
		runtime.WithPort(port),
		runtime.WithDiscoveryAddress(discovery),
		runtime.WithRoutingStrategy(runtime.RoundRobin),
		runtime.WithMetadata("version", "1.0.0"),
	)
	if err != nil {
		log.Fatalf("failed to create mesh service: %v", err)
	}

	svc.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from Go mesh service!",
			"service": name,
		})
	})

	svc.HandleFunc("GET /echo/{msg}", func(w http.ResponseWriter, r *http.Request) {
		msg := r.PathValue("msg")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"echo": msg,
		})
	})

	if err := svc.Run(context.Background()); err != nil {
		log.Fatalf("service error: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return fallback
}
