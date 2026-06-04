package config

import "time"

type Config struct {
	Service   ServiceConfig   `yaml:"service"`
	GRPC      GRPCConfig      `yaml:"grpc"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Resources ResourcesConfig `yaml:"resources"`
	Git       GitConfig       `yaml:"git"`
	Chunking  ChunkingConfig  `yaml:"chunking"`
	Filters   FiltersConfig   `yaml:"filters"`
	NATS      NATSConfig      `yaml:"nats"`

	AllowRequestOverrides []string `yaml:"allow_request_overrides"`
}

type ServiceConfig struct {
	Name     string `yaml:"name"`
	LogLevel string `yaml:"log_level"` // "debug" | "info" | "warn" | "error"
}

type GRPCConfig struct {
	Port             int  `yaml:"port"`
	AllowLocalStart  bool `yaml:"allow_local_start"`
}

type WorkspaceConfig struct {
	WorkDir           string `yaml:"work_dir"`
	CleanupOnComplete bool   `yaml:"cleanup_on_complete"`
}

type ResourcesConfig struct {
	MaxCPUCores int `yaml:"max_cpu_cores"` // 0 = NumCPU
	MaxRAMMB    int `yaml:"max_ram_mb"`    // 0 = no limit
}

type GitConfig struct {
	CloneTimeout         time.Duration `yaml:"-"`
	CloneTimeoutSeconds  int           `yaml:"clone_timeout_seconds"`
	CatFileBatchBufferKB int           `yaml:"cat_file_batch_buffer_kb"`
}

type ChunkingConfig struct {
	RowSizeTargetKB   int `yaml:"row_size_target_kb"`
	RowOverlapLines   int `yaml:"row_overlap_lines"`
	DiffContextLines  int `yaml:"diff_context_lines"`
	MaxFileSizeMB     int `yaml:"max_file_size_mb"`
}

type FiltersConfig struct {
	PathExclusions    []string `yaml:"path_exclusions"`
	BinaryExtensions  []string `yaml:"binary_extensions"`
}

type NATSConfig struct {
	URL        string             `yaml:"url"`
	Token      string             `yaml:"token"`
	JetStream  JetStreamConfig    `yaml:"jetstream"`
	Consumer   ConsumerConfig     `yaml:"consumer"`
}

type JetStreamConfig struct {
	RequestsStream         string `yaml:"requests_stream"`
	ChunksStream           string `yaml:"chunks_stream"`
	StatusStream           string `yaml:"status_stream"`
	PublishAckWaitSeconds  int    `yaml:"publish_ack_wait_seconds"`
}

type ConsumerConfig struct {
	RequestsSubject       string `yaml:"requests_subject"`
	CancelSubject         string `yaml:"cancel_subject"`
	QueueGroup            string `yaml:"queue_group"`
	RequestAckWaitSeconds int    `yaml:"request_ack_wait_seconds"`
	MaxInFlightScans      int    `yaml:"max_in_flight_scans"`
}
