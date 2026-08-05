package config

const (
	// ServiceManager is the init system this platform manages the server with.
	ServiceManager = "systemd"
	// DefaultServiceName is the systemd unit that runs the server.
	DefaultServiceName = "vllm.service"
	// DefaultServiceScope keeps the server running independently of any login
	// session.
	DefaultServiceScope = "system"
	// DefaultMemUtilization is vLLM's --gpu-memory-utilization: the fraction
	// of VRAM to reserve, leaving headroom for the KV cache.
	DefaultMemUtilization = "0.85"
)
