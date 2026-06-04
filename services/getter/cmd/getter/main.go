package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	grpcpkg "google.golang.org/grpc"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	kitscan "github.com/Harporis/harporis/kit/scan"

	"github.com/Harporis/harporis/services/getter/internal/config"
	"github.com/Harporis/harporis/services/getter/internal/filter"
	"github.com/Harporis/harporis/services/getter/internal/git"
	getgrpc "github.com/Harporis/harporis/services/getter/internal/grpc"
	"github.com/Harporis/harporis/services/getter/internal/metrics"
	getnats "github.com/Harporis/harporis/services/getter/internal/nats"
	"github.com/Harporis/harporis/services/getter/internal/resource"
	"github.com/Harporis/harporis/services/getter/internal/scan"
)

func main() {
	cfgPath := flag.String("config", "config/getter.yaml", "path to YAML config")
	metricsPort := flag.Int("metrics-port", 9100, "Prometheus /metrics port")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		fatal("config invalid: %v", err)
	}
	setupLogger(cfg.Service.LogLevel)
	resource.ApplyLimits(resource.Limits{
		MaxCPUCores: cfg.Resources.MaxCPUCores,
		MaxRAMMB:    cfg.Resources.MaxRAMMB,
	})
	metrics.Init()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cl, err := wire.Dial(wire.DialConfig{URL: cfg.NATS.URL, Token: cfg.NATS.Token, ClientName: "harporis-getter"})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()
	if err := wire.EnsureStreams(cl.JS); err != nil {
		fatal("nats streams: %v", err)
	}
	publisher := getnats.NewPublisher(cl.JS, cfg.NATS.JetStream.PublishAckWaitSeconds)

	registry := scan.NewRegistry()
	dispatch := buildDispatcher(cfg, publisher, registry)

	requestsSub, err := getnats.SubscribeRequests(rootCtx, cl.JS,
		getnats.RequestSubscribeOptions{
			AckWaitSeconds:   cfg.NATS.Consumer.RequestAckWaitSeconds,
			MaxInFlightScans: cfg.NATS.Consumer.MaxInFlightScans,
		}, dispatch)
	if err != nil {
		fatal("subscribe requests: %v", err)
	}

	cancelSub, err := getnats.SubscribeCancel(rootCtx, cl.NC,
		func(_ context.Context, req *v1.CancelScanRequest) {
			registry.Cancel(req.ScanId, req.Reason)
		})
	if err != nil {
		fatal("subscribe cancel: %v", err)
	}

	metricsSrv := metrics.ServeAsync(rootCtx, *metricsPort)

	grpcSrv, grpcLis := startGRPC(cfg)

	slog.Info("getter ready",
		"nats", cfg.NATS.URL, "grpc", grpcLis.Addr().String(), "metrics", *metricsPort)

	<-rootCtx.Done()
	slog.Info("shutdown initiated")
	shutdownCtx, sc := context.WithTimeout(context.Background(), 30*time.Second)
	defer sc()

	// Drain subscriptions so in-flight handlers (which may still be running
	// long scans, kept alive by heartbeats) get a chance to finish before
	// the connection closes. Drain returns immediately; the connection
	// stays open until the subscriptions report no more pending msgs.
	_ = requestsSub.Drain()
	_ = cancelSub.Drain()
	if err := cl.NC.Drain(); err != nil {
		slog.Warn("nats drain failed", "err", err)
	}

	// gRPC: GracefulStop allows in-flight RPCs to complete (Health is fast,
	// StartScanLocal is gated off in prod).
	grpcDone := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		grpcSrv.Stop() // hard stop on timeout
	}

	_ = metricsSrv.Shutdown(shutdownCtx)
	_ = grpcLis.Close()
}

func buildDispatcher(cfg *config.Config, pub *getnats.Publisher, reg *scan.Registry) func(context.Context, *v1.ScanRequest) error {
	return func(ctx context.Context, req *v1.ScanRequest) error {
		if err := kitscan.ValidateScanID(req.ScanId); err != nil {
			return fmt.Errorf("reject scan request: %w", err)
		}
		walkMode := walkModeFromProto(req.Type)
		if walkMode == "" {
			return fmt.Errorf("reject scan request: scan_type unset or unknown (%s)", req.Type.String())
		}
		source, err := sourceFromProto(req.Source)
		if err != nil {
			return fmt.Errorf("reject scan request: %w", err)
		}
		scanID := req.ScanId
		sc := scan.NewContext(scanID)
		runCtx, cancel := context.WithCancel(ctx)
		if err := reg.Register(scanID, sc, cancel); err != nil {
			cancel()
			return err
		}
		metrics.ActiveScans.Inc()
		defer func() {
			reg.Unregister(scanID)
			metrics.ActiveScans.Dec()
		}()

		repoDir, cleanup, err := git.PrepareRepo(runCtx, source, cfg.Workspace.WorkDir, cfg.Git.CloneTimeout)
		if err != nil {
			return err
		}
		defer func() {
			if cfg.Workspace.CleanupOnComplete {
				cleanup()
			}
		}()

		runner := scan.NewRunner(scan.RunnerConfig{
			ScanID:             scanID,
			RepoDir:            repoDir,
			WalkMode:           walkMode,
			Branch:             req.Range.GetBranch(),
			BaseBranch:         req.Range.GetBaseBranch(),
			Filter:             buildFilter(cfg, repoDir),
			Publisher:          pub,
			RowSizeTargetBytes: cfg.Chunking.RowSizeTargetKB * 1024,
			OverlapLines:       cfg.Chunking.RowOverlapLines,
			DiffContextLines:   cfg.Chunking.DiffContextLines,
			Workers:            cfg.Resources.MaxCPUCores,
			Output:             req.Output,
		})
		return runner.Run(runCtx)
	}
}

func buildFilter(cfg *config.Config, repoDir string) *filter.Filter {
	return &filter.Filter{
		PathExclusions:   cfg.Filters.PathExclusions,
		BinaryExtensions: filter.BuildExtensionSet(cfg.Filters.BinaryExtensions),
		MaxFileSize:      int64(cfg.Chunking.MaxFileSizeMB) * 1024 * 1024,
		GitAttrs:         loadGitAttributes(repoDir),
	}
}

func loadGitAttributes(repoDir string) *filter.GitAttributes {
	path := filepath.Join(repoDir, ".gitattributes")
	f, err := os.Open(path)
	if err != nil {
		// No .gitattributes is normal; absence => no rules.
		empty, _ := filter.ParseGitAttributes(strings.NewReader(""))
		return empty
	}
	defer f.Close()
	attrs, err := filter.ParseGitAttributes(f)
	if err != nil {
		slog.Warn("parse .gitattributes failed; ignoring", "path", path, "err", err)
		empty, _ := filter.ParseGitAttributes(strings.NewReader(""))
		return empty
	}
	return attrs
}

// sourceFromProto maps a ScanRequest.Source to the internal git.Source
// representation. Unset Source is rejected — every scan must declare what
// it scans; defaulting silently to '.' (process CWD) was a bad surprise.
func sourceFromProto(s *v1.Source) (git.Source, error) {
	if s == nil {
		return nil, fmt.Errorf("Source field is required")
	}
	if p := s.GetLocalPath(); p != "" {
		return git.LocalSource{Path: p}, nil
	}
	rem := s.GetRemote()
	if rem == nil || rem.Url == "" {
		return nil, fmt.Errorf("Source.remote.url is required when local_path is empty")
	}
	out := git.RemoteSource{URL: rem.Url}
	if tok := rem.GetToken(); tok != "" {
		out.Token = tok
	}
	if ssh := rem.GetSsh(); ssh != nil {
		out.SSHPrivateKeyPEM = []byte(ssh.PrivateKeyPem)
		out.SSHKnownHosts = []byte(ssh.KnownHosts)
	}
	return out, nil
}

func walkModeFromProto(t v1.ScanType) string {
	switch t {
	case v1.ScanType_FULL_HISTORY:
		return "full_history"
	case v1.ScanType_BRANCH_FULL:
		return "branch_full"
	case v1.ScanType_COMMIT_RANGE:
		return "commit_range"
	case v1.ScanType_CURRENT_STATE:
		return "current_state"
	case v1.ScanType_BRANCH_DIFF:
		return "branch_diff"
	case v1.ScanType_HEAD_DIFF:
		return "head_diff"
	case v1.ScanType_STAGED:
		return "staged"
	}
	return ""
}

func startGRPC(cfg *config.Config) (*grpcpkg.Server, net.Listener) {
	srv := getgrpc.New(getgrpc.Options{AllowLocalStart: cfg.GRPC.AllowLocalStart})
	gs := grpcpkg.NewServer()
	v1.RegisterGetterServiceServer(gs, srv)
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPC.Port))
	if err != nil {
		fatal("grpc listen: %v", err)
	}
	go gs.Serve(lis)
	return gs, lis
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
