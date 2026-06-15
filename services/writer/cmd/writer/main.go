// Package main is the writer service entrypoint. Mirrors the scanner main
// in structure: config → metrics+health → NATS dial → sink → consumer →
// N workers → HTTP server → wait → 30s graceful shutdown.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	kithealth "github.com/Harporis/harporis/kit/health"
	kithttpserver "github.com/Harporis/harporis/kit/metrics/httpserver"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/writer/internal/config"
	"github.com/Harporis/harporis/services/writer/internal/metrics"
	writernats "github.com/Harporis/harporis/services/writer/internal/nats"
	"github.com/Harporis/harporis/services/writer/internal/sink"
	"github.com/Harporis/harporis/services/writer/internal/version"
)

func main() {
	cfgPath := flag.String("config", "config/writer.yaml", "path to YAML config")
	workersFlag := flag.Int("workers", 0, "number of worker goroutines (overrides config)")
	outputDirFlag := flag.String("output-dir", "", "directory for NDJSON output (overrides config)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	if *workersFlag > 0 {
		cfg.Workers = *workersFlag
	}
	if *outputDirFlag != "" {
		cfg.OutputDir = *outputDirFlag
	}
	setupLogger(cfg.LogLevel)

	metrics.Init()
	metrics.BuildInfo.WithLabelValues(version.Version, version.Commit, version.ProtoVersion).Set(1)
	h := kithealth.New()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Sinks. NDJSON is the streaming default; SARIF is the standard
	// code-scanning industry format. Both write into cfg.OutputDir,
	// keyed by scan_id with distinct extensions (.ndjson / .sarif), so
	// the operator gets both views from one mount.
	batchCfg := sink.BatchConfig{
		FlushBatch:    cfg.FlushBatch,
		FlushInterval: time.Duration(cfg.FlushIntervalMs) * time.Millisecond,
	}
	// Per-scan affinity vs. per-replica suffix selection.
	//
	// With HARPORIS_FINDINGS_SHARDS<=1 (legacy): every replica may write
	// any scan, so HOSTNAME is stamped into per-scan filenames to keep
	// replicas from racing to rename onto the same path. NDJSON uses
	// O_APPEND so it stays plain `<scan>.ndjson`.
	//
	// With HARPORIS_FINDINGS_SHARDS>1 + REPLICA_INDEX set: this replica
	// owns exactly one shard via the durable subscription filter, so
	// every scan lands on a single replica — no clobbering possible,
	// filenames stay plain `<scan>.<ext>`.
	shardCount := wire.FindingsShardCount()
	shardIndex := 0
	if v := os.Getenv("HARPORIS_REPLICA_INDEX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n < shardCount {
			shardIndex = n
		} else {
			fatal("HARPORIS_REPLICA_INDEX=%q invalid (need 0..%d)", v, shardCount-1)
		}
	} else if shardCount > 1 {
		// docker compose --scale writer=N can't easily inject distinct
		// envs per container, but DOES name containers
		// `<project>-writer-<n>` (1-indexed). Fall back to parsing
		// HOSTNAME so `docker compose up -d --scale writer=N` works
		// out-of-the-box when HARPORIS_FINDINGS_SHARDS=N is also set.
		if idx, ok := parseReplicaIndexFromHostname(os.Getenv("HOSTNAME"), shardCount); ok {
			shardIndex = idx
		} else {
			fatal("HARPORIS_FINDINGS_SHARDS=%d but HARPORIS_REPLICA_INDEX is unset and HOSTNAME=%q does not match `<name>-N` pattern",
				shardCount, os.Getenv("HOSTNAME"))
		}
	}
	replicaID := ""
	if shardCount <= 1 {
		replicaID = os.Getenv("HOSTNAME")
	}
	slog.Info("findings affinity",
		"shards", shardCount,
		"replica_index", shardIndex,
		"filename_replica_suffix", replicaID,
	)

	sinks := make([]sink.Sink, 0, 6)
	if cfg.NDJSONEnabled != nil && *cfg.NDJSONEnabled {
		nd, err := sink.NewNDJSONFile(cfg.OutputDir)
		if err != nil {
			fatal("init ndjson sink: %v", err)
		}
		sinks = append(sinks, nd)
	}
	if cfg.SARIFEnabled != nil && *cfg.SARIFEnabled {
		sa, err := sink.NewSARIFConfig(cfg.OutputDir, batchCfg)
		if err != nil {
			fatal("init sarif sink: %v", err)
		}
		if replicaID != "" {
			sa.SetReplicaID(replicaID)
		}
		sinks = append(sinks, sa)
	}
	if cfg.HTMLEnabled != nil && *cfg.HTMLEnabled {
		hs, err := sink.NewHTMLConfig(cfg.OutputDir, batchCfg)
		if err != nil {
			fatal("init html sink: %v", err)
		}
		if cfg.MaskSecrets != nil && *cfg.MaskSecrets {
			hs.SetMaskSecrets(true)
		}
		if replicaID != "" {
			hs.SetReplicaID(replicaID)
		}
		sinks = append(sinks, hs)
	}
	if cfg.XLSXEnabled != nil && *cfg.XLSXEnabled {
		xs, err := sink.NewXLSXConfig(cfg.OutputDir, batchCfg)
		if err != nil {
			fatal("init xlsx sink: %v", err)
		}
		if replicaID != "" {
			xs.SetReplicaID(replicaID)
		}
		sinks = append(sinks, xs)
	}
	if cfg.PDFEnabled != nil && *cfg.PDFEnabled {
		ps, err := sink.NewPDFConfig(cfg.OutputDir, batchCfg)
		if err != nil {
			fatal("init pdf sink: %v", err)
		}
		if cfg.MaskSecrets != nil && *cfg.MaskSecrets {
			ps.SetMaskSecrets(true)
		}
		if replicaID != "" {
			ps.SetReplicaID(replicaID)
		}
		sinks = append(sinks, ps)
	}
	if cfg.SQLiteEnabled != nil && *cfg.SQLiteEnabled {
		sq, err := sink.NewSQLite(cfg.OutputDir)
		if err != nil {
			fatal("init sqlite sink: %v", err)
		}
		if replicaID != "" {
			sq.SetReplicaID(replicaID)
		}
		sinks = append(sinks, sq)
	}
	if cfg.ParquetEnabled != nil && *cfg.ParquetEnabled {
		pq, err := sink.NewParquetConfig(cfg.OutputDir, batchCfg)
		if err != nil {
			fatal("init parquet sink: %v", err)
		}
		if replicaID != "" {
			pq.SetReplicaID(replicaID)
		}
		sinks = append(sinks, pq)
	}
	if len(sinks) == 0 {
		fatal("no sinks enabled — set at least one of ndjson_enabled, sarif_enabled, html_enabled, xlsx_enabled, pdf_enabled, parquet_enabled, sqlite_enabled to true in writer.yaml")
	}

	// Sweep orphaned tempfiles from prior crashes mid-flush. The
	// accumulator sinks (SARIF/HTML/XLSX/PDF) write to a tempfile then
	// rename; a kill -9 between those steps leaves the tempfile behind.
	// Doing this once at startup keeps the output dir tidy without
	// pulling in a periodic janitor goroutine.
	swept, swErr := sink.SweepOrphanTempfiles(cfg.OutputDir, func(p string, err error) {
		slog.Warn("orphan tempfile sweep", "path", p, "err", err)
	})
	if swErr != nil {
		slog.Warn("orphan tempfile sweep returned error (continuing)", "err", swErr)
	}
	if swept > 0 {
		slog.Info("orphan tempfiles swept", "count", swept, "dir", cfg.OutputDir)
		metrics.OrphanTempfilesSwept.Add(float64(swept))
	}

	// Findings retention sweep. Both caps off by default; opt-in via
	// retention_age_hours / retention_max_bytes in writer.yaml. Runs
	// once on startup, then on a ticker until rootCtx is cancelled.
	retentionPolicy := sink.RetentionPolicy{
		TTL:      time.Duration(cfg.RetentionAgeHours) * time.Hour,
		MaxBytes: cfg.RetentionMaxBytes,
	}
	var retentionWG sync.WaitGroup
	if !retentionPolicy.Disabled() {
		retentionWG.Add(1)
		go func() {
			defer retentionWG.Done()
			runRetentionLoop(rootCtx, cfg.OutputDir, retentionPolicy, time.Duration(cfg.RetentionIntervalSeconds)*time.Second)
		}()
	}

	// NATS.
	cl, err := wire.Dial(wire.DialConfig{
		URL:        cfg.NATSURL,
		Token:      cfg.NATSToken,
		CredsFile:  cfg.NATSCredsFile,
		RootCAs:    cfg.NATSRootCAs,
		ClientName: "harporis-writer",
	})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()
	h.SetNATSConnected(true)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		fatal("ensure streams: %v", err)
	}

	consumer, err := writernats.NewFindingsConsumer(cl.JS, writernats.ConsumerOptions{
		BatchSize:      cfg.FetchBatch,
		FetchMaxWait:   time.Duration(cfg.FetchMaxWaitMs) * time.Millisecond,
		AckWaitSeconds: cfg.AckWaitSeconds,
		MaxDeliver:     cfg.MaxDeliver,
		MaxAckPending:  cfg.MaxAckPending,
		ShardIndex:     shardIndex,
		ShardCount:     shardCount,
	})
	if err != nil {
		fatal("create consumer: %v", err)
	}
	h.SetConsumerCreated(true)

	// Status consumer: listens for terminal ScanState events on
	// HARPORIS_STATUS and dispatches Finalize to every sink that
	// implements it (streaming Parquet, plus the accumulator sinks
	// drain their final batch deterministically instead of waiting on
	// the 2s ticker). Each writer replica gets an EPHEMERAL consumer
	// so finalisation is per-replica (a replica only knows about scans
	// it Wrote to).
	statusSub, err := writernats.NewStatusConsumer(cl.JS, version.Version)
	if err != nil {
		fatal("create status consumer: %v", err)
	}

	severitySet, err := cfg.SeveritySet()
	if err != nil {
		fatal("invalid severities config: %v", err)
	}

	// Worker goroutines.
	var workerWG sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workerWG.Add(1)
		go func(id int) {
			defer workerWG.Done()
			h.SetWorkerStarted(true)
			err := consumer.Run(rootCtx, func(ctx context.Context, f *v1.Finding) error {
				if !severitySet.Contains(f.Severity) {
					metrics.SinkSeverityDropped.WithLabelValues(f.Severity.String()).Inc()
					return nil
				}
				// Fan-out to enabled sinks, optionally narrowed by
				// f.OutputFormats — per-scan format selection set at
				// scan submission (ScanRequest.output.formats). Empty
				// list = write to every enabled sink (back-compat).
				wrote := 0
				for _, out := range sinks {
					if !sink.WantedByFinding(out, f.OutputFormats) {
						continue
					}
					wrote++
					start := time.Now()
					werr := out.Write(ctx, f)
					metrics.FindingsWriteSeconds.WithLabelValues(out.Name()).Observe(time.Since(start).Seconds())
					if werr != nil {
						metrics.SinkErrors.WithLabelValues(out.Name(), "write_error").Inc()
						return werr
					}
					metrics.SinkWrites.WithLabelValues(out.Name(), f.Severity.String()).Inc()
				}
				// Per-scan request asked for at least one format but no
				// enabled sink matched any of them (e.g. `-f pdf` while
				// pdf_enabled=false). Surface as a metric so operators
				// can see silent drops.
				if wrote == 0 && len(f.OutputFormats) > 0 {
					for _, req := range f.OutputFormats {
						metrics.SinkFormatIgnored.WithLabelValues(req).Inc()
					}
				}
				return nil
			})
			if err != nil {
				slog.Error("worker exit", "id", id, "err", err)
			}
		}(i)
	}

	// Status fan-out goroutine. Terminal events trigger a DELAYED
	// Finalize via time.AfterFunc — the cfg.FinalizeGraceMs grace
	// window buys the rest of the pipeline (scanner draining chunks
	// → publishing findings → writer Acking them) time to settle
	// after getter's "scan finished" event arrives.
	grace := time.Duration(cfg.FinalizeGraceMs) * time.Millisecond
	var statusWG sync.WaitGroup
	statusWG.Add(1)
	go func() {
		defer statusWG.Done()
		runErr := statusSub.Run(rootCtx, func(_ context.Context, scanID string, _ v1.ScanState) error {
			id := scanID
			time.AfterFunc(grace, func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				for _, out := range sinks {
					fin, ok := out.(sink.Finalizer)
					if !ok {
						continue
					}
					if err := fin.Finalize(ctx, id); err != nil {
						metrics.SinkErrors.WithLabelValues(out.Name(), "finalize_error").Inc()
						slog.Warn("finalize error", "sink", out.Name(), "scan_id", id, "err", err)
					}
				}
			})
			return nil
		})
		if runErr != nil {
			slog.Error("status consumer exit", "err", runErr)
		}
	}()

	// HTTP server: /metrics from writer, /healthz + /readyz from kit/health.
	srv := kithttpserver.ServeAsync(rootCtx, cfg.MetricsAddr, metrics.Handler(), h.HealthzHandler(), h.ReadyzHandler())

	slog.Info("writer ready",
		"nats", cfg.NATSURL, "workers", cfg.Workers, "metrics", cfg.MetricsAddr,
		"output_dir", cfg.OutputDir, "version", version.Version,
	)

	<-rootCtx.Done()
	slog.Info("shutdown initiated")
	shutdownCtx, sc := context.WithTimeout(context.Background(), 30*time.Second)
	defer sc()

	_ = consumer.Drain()
	_ = statusSub.Drain()

	done := make(chan struct{})
	go func() {
		workerWG.Wait()
		statusWG.Wait()
		retentionWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("workers drained")
	case <-shutdownCtx.Done():
		slog.Warn("worker drain timed out", "budget_s", 30)
	}

	for _, out := range sinks {
		if err := out.Close(); err != nil {
			slog.Warn("sink close", "sink", out.Name(), "err", err)
		}
	}
	if err := cl.NC.Drain(); err != nil {
		slog.Warn("nats drain", "err", err)
	}
	_ = srv.Shutdown(shutdownCtx)
}

// parseReplicaIndexFromHostname extracts a 0-based shard index from a
// compose-style hostname of shape `<anything>-<N>` where N is 1-based.
// Returns (idx, true) on success; (0, false) otherwise. shardCount is
// used to validate the parsed index falls inside [0, shardCount).
func parseReplicaIndexFromHostname(hostname string, shardCount int) (int, bool) {
	if hostname == "" {
		return 0, false
	}
	i := len(hostname)
	for i > 0 && hostname[i-1] >= '0' && hostname[i-1] <= '9' {
		i--
	}
	if i == len(hostname) || i == 0 || hostname[i-1] != '-' {
		return 0, false
	}
	n, err := strconv.Atoi(hostname[i:])
	if err != nil || n < 1 {
		return 0, false
	}
	idx := n - 1
	if idx < 0 || idx >= shardCount {
		return 0, false
	}
	return idx, true
}

// runRetentionLoop sweeps the findings dir on a ticker until ctx is
// cancelled, reporting reclaimed files/bytes via Prometheus.
func runRetentionLoop(ctx context.Context, rootDir string, policy sink.RetentionPolicy, interval time.Duration) {
	sweep := func() {
		stats, err := sink.SweepRetention(rootDir, policy, time.Now(), func(p string, err error) {
			slog.Warn("retention sweep", "path", p, "err", err)
		})
		if err != nil {
			slog.Warn("retention sweep error", "err", err)
		}
		if stats.RemovedByAge > 0 {
			metrics.RetentionSwept.WithLabelValues("age").Add(float64(stats.RemovedByAge))
		}
		if stats.RemovedBySize > 0 {
			metrics.RetentionSwept.WithLabelValues("size").Add(float64(stats.RemovedBySize))
		}
		if stats.BytesRemoved > 0 {
			// All bytes lump together when both reasons hit — operators
			// who want the split should diff the *_swept_total counters.
			metrics.RetentionBytesSwept.WithLabelValues("total").Add(float64(stats.BytesRemoved))
		}
		metrics.RetentionLastRunUnix.Set(float64(time.Now().Unix()))
		if stats.RemovedByAge+stats.RemovedBySize > 0 {
			slog.Info("findings retention swept",
				"by_age", stats.RemovedByAge,
				"by_size", stats.RemovedBySize,
				"bytes", stats.BytesRemoved,
				"remaining_files", stats.RemainingFiles,
				"remaining_bytes", stats.RemainingBytes,
			)
		}
	}
	sweep()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

func setupLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}
