package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad_ParsesValidYAML(t *testing.T) {
	yaml := `
service:
  name: getter
  log_level: info
grpc:
  port: 50051
  allow_local_start: false
workspace:
  work_dir: /tmp/wd
  cleanup_on_complete: true
resources:
  max_cpu_cores: 2
  max_ram_mb: 256
git:
  clone_timeout_seconds: 30
  cat_file_batch_buffer_kb: 32
chunking:
  row_size_target_kb: 256
  row_overlap_lines: 64
  diff_context_lines: 30
  max_file_size_mb: 10
filters:
  path_exclusions: [".git/"]
  binary_extensions: [".png"]
nats:
  url: nats://example:4222
  jetstream:
    requests_stream: REQ
    chunks_stream: CHK
    status_stream: STA
    publish_ack_wait_seconds: 5
  consumer:
    requests_subject: harporis.scans.requests
    cancel_subject: harporis.scans.cancel
    queue_group: getter-pool
    request_ack_wait_seconds: 60
    max_in_flight_scans: 4
`
	path := filepath.Join(t.TempDir(), "g.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "getter", cfg.Service.Name)
	require.Equal(t, 50051, cfg.GRPC.Port)
	require.Equal(t, 30*time.Second, cfg.Git.CloneTimeout)
	require.Equal(t, "nats://example:4222", cfg.NATS.URL)
}

func TestLoad_EnvSubstitution(t *testing.T) {
	t.Setenv("FOO_URL", "nats://from-env:4222")
	yaml := `
service:
  name: getter
  log_level: info
grpc:
  port: 50051
workspace:
  work_dir: /tmp
resources:
  max_cpu_cores: 1
  max_ram_mb: 64
git:
  clone_timeout_seconds: 1
chunking:
  row_size_target_kb: 1
  row_overlap_lines: 1
  diff_context_lines: 1
  max_file_size_mb: 1
nats:
  url: ${FOO_URL:-nats://default:4222}
  jetstream: { requests_stream: R, chunks_stream: C, status_stream: S, publish_ack_wait_seconds: 1 }
  consumer:  { requests_subject: x, cancel_subject: y, queue_group: g, request_ack_wait_seconds: 1, max_in_flight_scans: 1 }
`
	path := filepath.Join(t.TempDir(), "g.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "nats://from-env:4222", cfg.NATS.URL)
}

func TestLoad_EnvDefault(t *testing.T) {
	os.Unsetenv("MISSING_FOO")
	yaml := `
service: { name: getter, log_level: info }
grpc: { port: 1 }
workspace: { work_dir: /tmp }
resources: { max_cpu_cores: 1, max_ram_mb: 1 }
git: { clone_timeout_seconds: 1 }
chunking: { row_size_target_kb: 1, row_overlap_lines: 1, diff_context_lines: 1, max_file_size_mb: 1 }
nats:
  url: ${MISSING_FOO:-nats://default:4222}
  jetstream: { requests_stream: R, chunks_stream: C, status_stream: S, publish_ack_wait_seconds: 1 }
  consumer:  { requests_subject: x, cancel_subject: y, queue_group: g, request_ack_wait_seconds: 1, max_in_flight_scans: 1 }
`
	path := filepath.Join(t.TempDir(), "g.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "nats://default:4222", cfg.NATS.URL)
}
