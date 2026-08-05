//go:build !linux && !darwin

package config

const (
	ServiceManager        = "none"
	DefaultServiceName    = "vllm.service"
	DefaultServiceScope   = "user"
	DefaultMemUtilization = "0.85"
)
