package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

// servicePorts maps each in-stack service to the host:port its Prometheus
// /metrics endpoint listens on (inside the container, since v0.1.0 ports
// are no longer published to the host so --scale N works).
var servicePorts = map[string]int{"getter": 9100, "scanner": 9101}

func newMetricsCmd() *cobra.Command {
	var (
		service string
		url     string
		filter  string
		watch   bool
	)
	c := &cobra.Command{
		Use:   "metrics",
		Short: "fetch and filter a service's Prometheus /metrics (via docker compose exec)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			re, err := regexp.Compile(filter)
			if err != nil {
				return fmt.Errorf("bad --filter: %w", err)
			}
			fetch := func() error {
				if url != "" {
					return fetchAndPrintMetrics(url, re, cmd.OutOrStdout())
				}
				port, ok := servicePorts[service]
				if !ok {
					return fmt.Errorf("unknown --service %q (want one of: getter, scanner)", service)
				}
				co, err := compose.NewDefault()
				if err != nil {
					return fmt.Errorf("docker compose not available: %w (pass --url to bypass)", err)
				}
				return printMetricsFromCompose(co, service, port, re, cmd.OutOrStdout())
			}
			if !watch {
				return fetch()
			}
			for {
				if err := fetch(); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
				}
				time.Sleep(2 * time.Second)
			}
		},
	}
	c.Flags().StringVar(&service, "service", "getter", "stack service to probe: getter or scanner")
	c.Flags().StringVar(&url, "url", "", "explicit metrics URL (bypasses docker compose exec)")
	c.Flags().StringVar(&filter, "filter", "^harporis_|^scanner_", "regex applied to each metric line")
	c.Flags().BoolVar(&watch, "watch", false, "refresh every 2 seconds")
	return c
}

func fetchAndPrintMetrics(url string, re *regexp.Regexp, w io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return printFilteredMetrics(resp.Body, re, w)
}

func printMetricsFromCompose(co *compose.Compose, service string, port int, re *regexp.Regexp, w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := co.Exec(ctx, service, "wget", "-qO-", fmt.Sprintf("http://localhost:%d/metrics", port))
	if err != nil {
		return fmt.Errorf("compose exec %s: %w", service, err)
	}
	return printFilteredMetrics(strings.NewReader(body), re, w)
}

func printFilteredMetrics(body io.Reader, re *regexp.Regexp, w io.Writer) error {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if re.MatchString(line) {
			fmt.Fprintln(w, line)
		}
	}
	return scanner.Err()
}
