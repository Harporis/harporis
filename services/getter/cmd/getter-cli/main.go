// getter-cli is a small operator tool for sending ScanRequests, cancelling
// active scans, and watching status events. It talks NATS directly — there
// is no gRPC dependency here, so it works against any cluster the operator
// has connection access to.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/wire"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		cmdScan(os.Args[2:])
	case "cancel":
		cmdCancel(os.Args[2:])
	case "watch":
		cmdWatch(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `getter-cli — submit, cancel, and watch Harporis getter scans

USAGE
  getter-cli scan    [flags]
  getter-cli cancel  <scan-id> [flags]
  getter-cli watch   <scan-id> [flags]

COMMON FLAGS
  --nats <url>   NATS server URL (default: $NATS_URL or nats://localhost:4222)

SCAN FLAGS
  --type <name>          current_state|full_history|branch_full|commit_range|
                         branch_diff|head_diff|staged (default: current_state)
  --scan-id <id>         Custom scan id (default: random UUID)
  --local <path>         Local repo path (inside the getter container/host)
  --remote-url <url>     Remote repo URL (https:// or git@host:repo.git)
  --remote-token <tok>   Bearer / PAT for HTTPS remotes
  --remote-ssh-key <p>   Path to SSH private key file (PEM)
  --remote-known-hosts <p>  Path to known_hosts file (pins host key check)
  --branch <name>        For branch_full / branch_diff
  --base-branch <name>   For branch_diff
  --from <sha>           For commit_range (exclusive)
  --to <sha>             For commit_range (inclusive)
  --wait                 Block on status stream until terminal state

CANCEL FLAGS
  --reason <text>        Free-form reason shown in the final status event

EXAMPLES
  getter-cli scan --local /tmp/myrepo --wait
  getter-cli scan --type full_history --remote-url https://github.com/foo/bar.git --remote-token ghp_xxx
  getter-cli cancel scan-abc --reason "changed my mind"
  getter-cli watch scan-abc`)
}

// ----- subcommands -----

func cmdScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	natsURL := fs.String("nats", defaultNATSURL(), "NATS URL")
	scanID := fs.String("scan-id", "", "scan id (default: generated UUID)")
	scanType := fs.String("type", "current_state", "scan type")
	localPath := fs.String("local", "", "local repo path")
	remoteURL := fs.String("remote-url", "", "remote repo URL")
	remoteToken := fs.String("remote-token", "", "Bearer / PAT token")
	remoteSSHKey := fs.String("remote-ssh-key", "", "path to SSH private key file")
	remoteKnownHosts := fs.String("remote-known-hosts", "", "path to known_hosts file")
	branch := fs.String("branch", "", "branch name (branch modes)")
	baseBranch := fs.String("base-branch", "", "base branch (branch_diff)")
	from := fs.String("from", "", "commit from (commit_range)")
	to := fs.String("to", "", "commit to (commit_range)")
	wait := fs.Bool("wait", false, "block until terminal status received")
	_ = fs.Parse(args)

	if *scanID == "" {
		*scanID = uuid.NewString()
	}
	typ, ok := scanTypeFromString(*scanType)
	if !ok {
		fatal("invalid --type %q", *scanType)
	}
	source, err := buildSource(*localPath, *remoteURL, *remoteToken, *remoteSSHKey, *remoteKnownHosts)
	if err != nil {
		fatal("%v", err)
	}

	req := &v1.ScanRequest{
		ScanId: *scanID,
		Type:   typ,
		Source: source,
	}
	if *branch != "" || *baseBranch != "" || *from != "" || *to != "" {
		req.Range = &v1.ScanRange{
			Branch: *branch, BaseBranch: *baseBranch,
			CommitFrom: *from, CommitTo: *to,
		}
	}

	cl, err := wire.Dial(wire.DialConfig{URL: *natsURL, ClientName: "harporis-getter-cli"})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()
	// Ensure streams exist so a first-run client doesn't fail silently
	// when the getter hasn't created them yet.
	if err := wire.EnsureStreams(cl.JS); err != nil {
		fatal("ensure streams: %v", err)
	}

	data, err := proto.Marshal(req)
	if err != nil {
		fatal("marshal scan request: %v", err)
	}
	if _, err := cl.JS.Publish(wire.ScansRequestsSubject, data); err != nil {
		fatal("publish: %v", err)
	}
	fmt.Printf("submitted scan_id=%s type=%s\n", req.ScanId, typ.String())

	if *wait {
		watchStatus(cl, req.ScanId, 30*time.Minute)
	}
}

func cmdCancel(args []string) {
	fs := flag.NewFlagSet("cancel", flag.ExitOnError)
	natsURL := fs.String("nats", defaultNATSURL(), "NATS URL")
	reason := fs.String("reason", "operator cancelled", "free-form cancellation reason")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		fatal("usage: getter-cli cancel <scan-id>")
	}
	scanID := rest[0]

	cl, err := wire.Dial(wire.DialConfig{URL: *natsURL, ClientName: "harporis-getter-cli"})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()

	req := &v1.CancelScanRequest{ScanId: scanID, Reason: *reason}
	data, _ := proto.Marshal(req)
	if err := cl.NC.Publish(wire.ScansCancelSubject, data); err != nil {
		fatal("publish cancel: %v", err)
	}
	if err := cl.NC.Flush(); err != nil {
		fatal("flush: %v", err)
	}
	fmt.Printf("cancel sent scan_id=%s reason=%q\n", scanID, *reason)
}

func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	natsURL := fs.String("nats", defaultNATSURL(), "NATS URL")
	timeout := fs.Duration("timeout", 30*time.Minute, "stop watching after this much idle time")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) != 1 {
		fatal("usage: getter-cli watch <scan-id>")
	}
	scanID := rest[0]

	cl, err := wire.Dial(wire.DialConfig{URL: *natsURL, ClientName: "harporis-getter-cli-watch"})
	if err != nil {
		fatal("nats dial: %v", err)
	}
	defer cl.Close()
	if err := wire.EnsureStreams(cl.JS); err != nil {
		fatal("ensure streams: %v", err)
	}
	watchStatus(cl, scanID, *timeout)
}

// ----- helpers -----

func defaultNATSURL() string {
	if v := os.Getenv("NATS_URL"); v != "" {
		return v
	}
	return "nats://localhost:4222"
}

func scanTypeFromString(s string) (v1.ScanType, bool) {
	switch strings.ToLower(s) {
	case "current_state":
		return v1.ScanType_CURRENT_STATE, true
	case "full_history":
		return v1.ScanType_FULL_HISTORY, true
	case "branch_full":
		return v1.ScanType_BRANCH_FULL, true
	case "commit_range":
		return v1.ScanType_COMMIT_RANGE, true
	case "branch_diff":
		return v1.ScanType_BRANCH_DIFF, true
	case "head_diff":
		return v1.ScanType_HEAD_DIFF, true
	case "staged":
		return v1.ScanType_STAGED, true
	}
	return 0, false
}

func buildSource(localPath, remoteURL, token, sshKeyPath, knownHostsPath string) (*v1.Source, error) {
	if localPath != "" {
		if remoteURL != "" {
			return nil, errors.New("--local and --remote-url are mutually exclusive")
		}
		return &v1.Source{Src: &v1.Source_LocalPath{LocalPath: localPath}}, nil
	}
	if remoteURL == "" {
		return nil, errors.New("either --local or --remote-url is required")
	}
	rr := &v1.RemoteRepo{Url: remoteURL}
	switch {
	case token != "" && sshKeyPath != "":
		return nil, errors.New("--remote-token and --remote-ssh-key are mutually exclusive")
	case token != "":
		rr.Auth = &v1.RemoteRepo_Token{Token: token}
	case sshKeyPath != "":
		ssh := &v1.SshAuth{}
		keyData, err := os.ReadFile(sshKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", sshKeyPath, err)
		}
		ssh.PrivateKeyPem = string(keyData)
		if knownHostsPath != "" {
			khData, err := os.ReadFile(knownHostsPath)
			if err != nil {
				return nil, fmt.Errorf("read known_hosts %s: %w", knownHostsPath, err)
			}
			ssh.KnownHosts = string(khData)
		}
		rr.Auth = &v1.RemoteRepo_Ssh{Ssh: ssh}
	}
	return &v1.Source{Src: &v1.Source_Remote{Remote: rr}}, nil
}

func watchStatus(cl *wire.Client, scanID string, idleTimeout time.Duration) {
	// Use an ephemeral pull consumer scoped to this scan's status subject.
	// "cli-watch-<scanID>" is unique enough; durables aren't useful here —
	// when the CLI exits, the consumer can be reaped on the next maintenance run.
	consumerName := "cli-watch-" + sanitizeID(scanID)
	sub, err := cl.JS.PullSubscribe(wire.StatusSubject(scanID), consumerName,
		natsclient.BindStream(wire.StatusStream))
	if err != nil {
		fatal("subscribe status: %v", err)
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = cl.JS.DeleteConsumer(wire.StatusStream, consumerName)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastSeen := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastSeen) > idleTimeout {
			fmt.Fprintf(os.Stderr, "watch: idle timeout (%s) — no status events for %s\n", idleTimeout, scanID)
			return
		}
		msgs, err := sub.Fetch(8, natsclient.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
				continue
			}
			log.Printf("watch fetch: %v", err)
			continue
		}
		for _, m := range msgs {
			lastSeen = time.Now()
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err != nil {
				log.Printf("watch unmarshal: %v", err)
				_ = m.Ack()
				continue
			}
			printStatusEvent(&ev)
			_ = m.Ack()
			if isTerminal(ev.State) {
				return
			}
		}
	}
}

func printStatusEvent(ev *v1.StatusEvent) {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format(time.RFC3339)
	m := ev.GetMetrics()
	fmt.Printf("[%s] %-9s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, ev.State.String(), ev.Message,
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
}

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_FAILED, v1.ScanState_CANCELLED, v1.ScanState_PARTIAL:
		return true
	}
	return false
}

func sanitizeID(s string) string {
	// NATS consumer names allow [A-Za-z0-9_-]; the ScanID validator already
	// restricts to that set, so this is a defence-in-depth no-op.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "getter-cli: "+format+"\n", args...)
	os.Exit(1)
}
