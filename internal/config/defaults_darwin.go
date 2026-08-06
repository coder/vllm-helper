package config

const (
	// ServiceManager is the init system this platform manages the server with.
	ServiceManager = "launchd"
	// DefaultServiceName is a launchd label (reverse-DNS), not a unit file.
	DefaultServiceName = "com.coder.vllm"
	// DefaultServiceScope is user scope: a LaunchAgent under ~/Library needs
	// no root, unlike a LaunchDaemon.
	DefaultServiceScope = "user"
	// DefaultMemUtilization is deliberately far below the Linux default.
	// macOS only has vLLM's CPU backend, where --gpu-memory-utilization
	// reserves a fraction of *system* RAM and vLLM refuses to start unless
	// that much is free right now. On a desktop with a browser open, 0.85
	// never fits.
	DefaultMemUtilization = "0.3"
)
