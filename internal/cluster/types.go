package cluster

// Mode is how a profile uses multiple nodes.
type Mode string

const (
	ModeNone     Mode = "none"
	ModeReplicas Mode = "replicas"
	ModeSharded  Mode = "sharded"
)

// RouterConfig configures the local router in replicas mode.
type RouterConfig struct {
	Bind   string // host:port
	Sticky string // auto|authorization|header:<Name>
}

// DistConfig configures a distributed (sharded) launch.
type DistConfig struct {
	Backend string // ray (future backends may exist)
	PP      int
	TP      int
	IFace   string // NIC name for NCCL/RCCL binding
}

// Config is saved on a profile to record multi-node settings.
type Config struct {
	Mode    Mode
	Members []string
	Router  RouterConfig
	Dist    DistConfig
}
