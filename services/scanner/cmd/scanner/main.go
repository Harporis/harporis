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
	"github.com/Harporis/harporis/services/scanner/internal/config"
	"github.com/Harporis/harporis/services/scanner/internal/detect"
	"github.com/Harporis/harporis/services/scanner/internal/metrics"
	scannernats "github.com/Harporis/harporis/services/scanner/internal/nats"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
	"github.com/Harporis/harporis/services/scanner/internal/rulewatch"
	"github.com/Harporis/harporis/services/scanner/internal/status"
	"github.com/Harporis/harporis/services/scanner/internal/version"
	"github.com/Harporis/harporis/services/scanner/internal/worker"
)

func main() {
	cfgPath := flag.String("config", "config/scanner.yaml", "path to YAML config")
	rulesPath := flag.String("rules", "", "path to YAML rule pack (default: embedded)")
	workersFlag := flag.Int("workers", 0, "number of worker goroutines (overrides config)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	if *workersFlag > 0 {
		cfg.Workers = *workersFlag
	}
	if *rulesPath != "" {
		cfg.RulesPath = *rulesPath
	}
	setupLogger(cfg.LogLevel)

	// Rules. cfg.RulesPath == "" means use the embedded pack (no
	// hot-reload). When the path is set, we wire a rulewatch.Watcher
	// that polls mtime every 5s and atomic-swaps the detector when
	// the operator edits the YAML.
	var ruleSet []rules.Rule
	if cfg.RulesPath != "" {
		ruleSet, err = rules.LoadFile(cfg.RulesPath)
	} else {
		ruleSet, err = rules.LoadEmbedded()
	}
	if err != nil {
		fatal("load rules: %v", err)
	}
	if err := rules.Validate(ruleSet); err != nil {
		fatal("invalid rule pack: %v", err)
	}
	slog.Info("rule pack loaded", "rules", len(ruleSet), "path", cfg.RulesPath)

	// Metrics + health.
	metrics.Init()
	metrics.BuildInfo.WithLabelValues(version.Version, version.Commit, version.ProtoVersion).Set(1)
	h := kithealth.New()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// NATS.
	cl, err := wire.Dial(wire.DialConfig{
		URL:        cfg.NATSURL,
		Token:      cfg.NATSToken,
		CredsFile:  cfg.NATSCredsFile,
		RootCAs:    cfg.NATSRootCAs,
		ClientName: "harporis-scanner",
	})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()
	h.SetNATSConnected(true)

	if err := wire.EnsureStreams(cl.JS); err != nil {
		fatal("ensure streams: %v", err)
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	pub := scannernats.NewPublisher(cl.JS, time.Duration(cfg.PublishAckWaitSeconds)*time.Second, "scanner-"+host)

	// Status tracker.
	tracker := status.NewTracker(pub, time.Duration(cfg.StatusTickMs)*time.Millisecond)
	var workerWG sync.WaitGroup
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		tracker.Run(rootCtx)
	}()

	// Detector + handler. If cfg.RulesPath is set, wrap the detector
	// in a rulewatch.Watcher so an edit to the YAML pack on disk is
	// picked up by future scans without a scanner restart.
	var dp worker.DetectorProvider
	if cfg.RulesPath != "" {
		w, err := rulewatch.NewWatcher(cfg.RulesPath, version.String())
		if err != nil {
			fatal("rules watcher: %v", err)
		}
		go w.Run(rootCtx, 5*time.Second)
		dp = w
	} else {
		dp = worker.Static(detect.NewDetector(ruleSet, version.String()))
	}
	handler := worker.NewHandler(dp, pub, tracker)

	// Consumer.
	consumer, err := scannernats.NewChunksConsumer(cl.JS, scannernats.ConsumerOptions{
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
	for i := 0; i < cfg.Workers; i++ {
		workerWG.Add(1)
		go func(id int) {
			defer workerWG.Done()
			h.SetWorkerStarted(true)
			err := consumer.Run(rootCtx, func(ctx context.Context, c *v1.GitRowChunk) error {
				return handler.Handle(ctx, c)
			})
			if err != nil {
				slog.Error("worker exit", "id", id, "err", err)
			}
		}(i)
	}

	// HTTP server: /metrics from scanner, /healthz + /readyz from kit/health.
	srv := kithttpserver.ServeAsync(rootCtx, cfg.MetricsAddr, metrics.Handler(), h.HealthzHandler(), h.ReadyzHandler())

	slog.Info("scanner ready",
		"nats", cfg.NATSURL, "workers", cfg.Workers, "metrics", cfg.MetricsAddr,
		"version", version.Version, "rules", len(ruleSet),
	)

	<-rootCtx.Done()
	slog.Info("shutdown initiated")
	shutdownCtx, sc := context.WithTimeout(context.Background(), 30*time.Second)
	defer sc()

	// Drain stops further fetches; in-flight handlers continue.
	_ = consumer.Drain()

	// Wait for worker goroutines, bounded by shutdownCtx.
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
