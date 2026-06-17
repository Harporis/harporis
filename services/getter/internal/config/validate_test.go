package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func validBaseConfig() *Config {
	return &Config{
		Service:   ServiceConfig{Name: "getter", LogLevel: "info"},
		GRPC:      GRPCConfig{Port: 50051},
		Workspace: WorkspaceConfig{WorkDir: "/tmp/wd"},
		Resources: ResourcesConfig{MaxCPUCores: 4, MaxRAMMB: 256},
		Git:       GitConfig{CloneTimeoutSeconds: 60, CatFileBatchBufferKB: 64},
		Chunking: ChunkingConfig{
			RowSizeTargetKB:  256,
			RowOverlapLines:  64,
			DiffContextLines: 30,
			MaxFileSizeMB:    10,
		},
		NATS: NATSConfig{
			URL: "nats://example:4222",
			JetStream: JetStreamConfig{
				RequestsStream: "R", ChunksStream: "C", StatusStream: "S",
				PublishAckWaitSeconds: 5,
			},
			Consumer: ConsumerConfig{
				RequestsSubject: "x", CancelSubject: "y",
				QueueGroup: "g", RequestAckWaitSeconds: 60, MaxInFlightScans: 4,
				MaxDeliver: 5, NakBackoffSeconds: 5,
			},
		},
	}
}

func TestValidate_OK(t *testing.T) {
	require.NoError(t, Validate(validBaseConfig()))
}

func TestValidate_Errors(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Config)
		errSubstr string
	}{
		{"empty service name", func(c *Config) { c.Service.Name = "" }, "service.name"},
		{"bad log level", func(c *Config) { c.Service.LogLevel = "verbose" }, "service.log_level"},
		{"port out of range", func(c *Config) { c.GRPC.Port = 0 }, "grpc.port"},
		{"empty workdir", func(c *Config) { c.Workspace.WorkDir = "" }, "workspace.work_dir"},
		{"negative cpu", func(c *Config) { c.Resources.MaxCPUCores = -1 }, "resources.max_cpu_cores"},
		{"negative ram", func(c *Config) { c.Resources.MaxRAMMB = -1 }, "resources.max_ram_mb"},
		{"zero row size", func(c *Config) { c.Chunking.RowSizeTargetKB = 0 }, "chunking.row_size_target_kb"},
		{"overlap > row", func(c *Config) { c.Chunking.RowOverlapLines = 1_000_000 }, "row_overlap_lines"},
		{"zero max file", func(c *Config) { c.Chunking.MaxFileSizeMB = 0 }, "chunking.max_file_size_mb"},
		{"empty NATS URL", func(c *Config) { c.NATS.URL = "" }, "nats.url"},
		{"empty queue group", func(c *Config) { c.NATS.Consumer.QueueGroup = "" }, "queue_group"},
		{"low ack wait", func(c *Config) { c.NATS.Consumer.RequestAckWaitSeconds = 0 }, "request_ack_wait_seconds"},
		{"low publish ack wait", func(c *Config) { c.NATS.JetStream.PublishAckWaitSeconds = 0 }, "publish_ack_wait_seconds"},
		{"low max in flight", func(c *Config) { c.NATS.Consumer.MaxInFlightScans = 0 }, "max_in_flight_scans"},
		{"low max deliver", func(c *Config) { c.NATS.Consumer.MaxDeliver = 0 }, "max_deliver"},
		{"negative nak backoff", func(c *Config) { c.NATS.Consumer.NakBackoffSeconds = -1 }, "nak_backoff_seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBaseConfig()
			tc.mutate(cfg)
			err := Validate(cfg)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errSubstr)
		})
	}
}
