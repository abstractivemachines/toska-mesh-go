package runtime

import "time"

// LoadBalancingStrategy controls how the router distributes traffic.
type LoadBalancingStrategy string

const (
	RoundRobin         LoadBalancingStrategy = "RoundRobin"
	LeastConnections   LoadBalancingStrategy = "LeastConnections"
	Random             LoadBalancingStrategy = "Random"
	WeightedRoundRobin LoadBalancingStrategy = "WeightedRoundRobin"
	IPHash             LoadBalancingStrategy = "IPHash"
)

// RoutingOptions controls how this service is routed to by the mesh gateway.
type RoutingOptions struct {
	Scheme              string                // URL scheme ("http" or "https"). Default: "http".
	HealthCheckEndpoint string                // Health endpoint path. Default: matches ServiceOptions.HealthEndpoint.
	Strategy            LoadBalancingStrategy // Load balancing strategy. Default: RoundRobin.
	Weight              int                   // Weight for WeightedRoundRobin. Default: 1.
}

// ServiceOptions configures a mesh service instance.
type ServiceOptions struct {
	ServiceName string // Name registered with discovery. Required.
	ServiceID   string // Unique instance ID. Auto-generated if empty.

	Address           string // Bind address. Default: "0.0.0.0".
	AdvertisedAddress string // Address advertised to discovery. Defaults to Address.
	Port              int    // Bind port. 0 = ephemeral (useful for tests).

	HealthEndpoint     string        // Health endpoint path. Default: "/health".
	HealthInterval     time.Duration // Probe interval. Default: 30s.
	HealthTimeout      time.Duration // Probe timeout. Default: 5s.
	UnhealthyThreshold int           // Failed probes before unhealthy. Default: 3.

	HeartbeatEnabled bool // Send periodic heartbeats to discovery. Default: true.
	AutoRegister     bool // Register on startup. Default: true.

	DiscoveryAddress string // gRPC address of discovery service. Default: "localhost:8080".

	Metadata map[string]string // Custom metadata propagated to discovery.
	Routing  RoutingOptions    // Routing configuration.
}

// Option is a functional option for configuring a MeshService.
type Option func(*ServiceOptions)

// DefaultOptions returns ServiceOptions with sensible defaults.
func DefaultOptions() ServiceOptions {
	return ServiceOptions{
		ServiceName:        "mesh-service",
		Address:            "0.0.0.0",
		Port:               8080,
		HealthEndpoint:     "/health",
		HealthInterval:     30 * time.Second,
		HealthTimeout:      5 * time.Second,
		UnhealthyThreshold: 3,
		HeartbeatEnabled:   true,
		AutoRegister:       true,
		DiscoveryAddress:   "localhost:8080",
		Metadata:           make(map[string]string),
		Routing: RoutingOptions{
			Scheme:   "http",
			Strategy: RoundRobin,
			Weight:   1,
		},
	}
}

func WithServiceName(name string) Option {
	return func(o *ServiceOptions) { o.ServiceName = name }
}

func WithServiceID(id string) Option {
	return func(o *ServiceOptions) { o.ServiceID = id }
}

func WithAddress(addr string) Option {
	return func(o *ServiceOptions) { o.Address = addr }
}

func WithAdvertisedAddress(addr string) Option {
	return func(o *ServiceOptions) { o.AdvertisedAddress = addr }
}

func WithPort(port int) Option {
	return func(o *ServiceOptions) { o.Port = port }
}

func WithHealthEndpoint(endpoint string) Option {
	return func(o *ServiceOptions) { o.HealthEndpoint = endpoint }
}

func WithHealthInterval(d time.Duration) Option {
	return func(o *ServiceOptions) { o.HealthInterval = d }
}

func WithHeartbeat(enabled bool) Option {
	return func(o *ServiceOptions) { o.HeartbeatEnabled = enabled }
}

func WithAutoRegister(enabled bool) Option {
	return func(o *ServiceOptions) { o.AutoRegister = enabled }
}

func WithDiscoveryAddress(addr string) Option {
	return func(o *ServiceOptions) { o.DiscoveryAddress = addr }
}

func WithMetadata(key, value string) Option {
	return func(o *ServiceOptions) { o.Metadata[key] = value }
}

func WithRoutingStrategy(s LoadBalancingStrategy) Option {
	return func(o *ServiceOptions) { o.Routing.Strategy = s }
}

func WithRoutingWeight(w int) Option {
	return func(o *ServiceOptions) { o.Routing.Weight = w }
}

func WithRoutingScheme(scheme string) Option {
	return func(o *ServiceOptions) { o.Routing.Scheme = scheme }
}
