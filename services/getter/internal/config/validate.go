package config

import (
	"errors"
	"fmt"
)

func Validate(c *Config) error {
	var errs []error
	if c.Service.Name == "" {
		errs = append(errs, errors.New("service.name: must not be empty"))
	}
	switch c.Service.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("service.log_level: invalid %q", c.Service.LogLevel))
	}
	if c.GRPC.Port < 1 || c.GRPC.Port > 65535 {
		errs = append(errs, fmt.Errorf("grpc.port: out of range: %d", c.GRPC.Port))
	}
	if c.Workspace.WorkDir == "" {
		errs = append(errs, errors.New("workspace.work_dir: must not be empty"))
	}
	if c.Resources.MaxCPUCores < 0 {
		errs = append(errs, errors.New("resources.max_cpu_cores: must be >= 0"))
	}
	if c.Resources.MaxRAMMB < 0 {
		errs = append(errs, errors.New("resources.max_ram_mb: must be >= 0"))
	}
	if c.Chunking.RowSizeTargetKB <= 0 {
		errs = append(errs, errors.New("chunking.row_size_target_kb: must be > 0"))
	}
	// approximate: assume avg 100 bytes/line; overlap must be < row capacity
	maxOverlapLines := (c.Chunking.RowSizeTargetKB * 1024) / 100
	if c.Chunking.RowOverlapLines >= maxOverlapLines {
		errs = append(errs, fmt.Errorf("chunking.row_overlap_lines: must be < row capacity (~%d lines)", maxOverlapLines))
	}
	if c.Chunking.MaxFileSizeMB <= 0 {
		errs = append(errs, errors.New("chunking.max_file_size_mb: must be > 0"))
	}
	if c.Chunking.DiffContextLines < 0 {
		errs = append(errs, fmt.Errorf("chunking.diff_context_lines: must be >= 0 (got %d)", c.Chunking.DiffContextLines))
	}
	if c.NATS.URL == "" {
		errs = append(errs, errors.New("nats.url: must not be empty"))
	}
	if c.NATS.Consumer.QueueGroup == "" {
		errs = append(errs, errors.New("nats.consumer.queue_group: must not be empty"))
	}
	if c.NATS.Consumer.RequestAckWaitSeconds < 5 {
		errs = append(errs, fmt.Errorf("nats.consumer.request_ack_wait_seconds: must be >= 5 (got %d)", c.NATS.Consumer.RequestAckWaitSeconds))
	}
	if c.NATS.JetStream.PublishAckWaitSeconds < 1 {
		errs = append(errs, fmt.Errorf("nats.jetstream.publish_ack_wait_seconds: must be >= 1 (got %d)", c.NATS.JetStream.PublishAckWaitSeconds))
	}
	if c.NATS.Consumer.MaxInFlightScans < 1 {
		errs = append(errs, fmt.Errorf("nats.consumer.max_in_flight_scans: must be >= 1 (got %d)", c.NATS.Consumer.MaxInFlightScans))
	}
	if c.NATS.Consumer.MaxDeliver < 1 {
		errs = append(errs, fmt.Errorf("nats.consumer.max_deliver: must be >= 1 (got %d)", c.NATS.Consumer.MaxDeliver))
	}
	if c.NATS.Consumer.NakBackoffSeconds < 0 {
		errs = append(errs, fmt.Errorf("nats.consumer.nak_backoff_seconds: must be >= 0 (got %d)", c.NATS.Consumer.NakBackoffSeconds))
	}
	return errors.Join(errs...)
}
