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
	sinks := make([]sink.Sink, 0, 2)
	if cfg.NDJSONEnabled != nil && *cfg.NDJSONEnabled {
		nd, err := sink.NewNDJSONFile(cfg.OutputDir)
		if err != nil {
			fatal("init ndjson sink: %v", err)
		}
		sinks = append(sinks, nd)
	}
	if cfg.SARIFEnabled != nil && *cfg.SARIFEnabled {
		sa, err := sink.NewSARIF(cfg.OutputDir)
		if err != nil {
			fatal("init sarif sink: %v", err)
		}
		sinks = append(sinks, sa)
	}
	if cfg.HTMLEnabled != nil && *cfg.HTMLEnabled {
		hs, err := sink.NewHTML(cfg.OutputDir)
		if err != nil {
			fatal("init html sink: %v", err)
		}
		if cfg.MaskSecrets != nil && *cfg.MaskSecrets {
			hs.SetMaskSecrets(true)
		}
		sinks = append(sinks, hs)
	}
	if cfg.XLSXEnabled != nil && *cfg.XLSXEnabled {
		xs, err := sink.NewXLSX(cfg.OutputDir)
		if err != nil {
			fatal("init xlsx sink: %v", err)
		}
		sinks = append(sinks, xs)
	}
	if cfg.PDFEnabled != nil && *cfg.PDFEnabled {
		ps, err := sink.NewPDF(cfg.OutputDir)
		if err != nil {
			fatal("init pdf sink: %v", err)
		}
		if cfg.MaskSecrets != nil && *cfg.MaskSecrets {
			ps.SetMaskSecrets(true)
		}
		sinks = append(sinks, ps)
	}
	if cfg.ParquetEnabled != nil && *cfg.ParquetEnabled {
		pq, err := sink.NewParquet(cfg.OutputDir)
		if err != nil {
			fatal("init parquet sink: %v", err)
		}
		sinks = append(sinks, pq)
	}
	if len(sinks) == 0 {
		fatal("no sinks enabled — set ndjson_enabled or sarif_enabled to true")
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
	})
	if err != nil {
		fatal("create consumer: %v", err)
	}
	h.SetConsumerCreated(true)

	// Worker goroutines.
	var workerWG sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workerWG.Add(1)
		go func(id int) {
			defer workerWG.Done()
			h.SetWorkerStarted(true)
			err := consumer.Run(rootCtx, func(ctx context.Context, f *v1.Finding) error {
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

	done := make(chan struct{})
	go func() {
		workerWG.Wait()
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
