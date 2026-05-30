package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	var (
		url    string
		filter string
		watch  bool
	)
	c := &cobra.Command{
		Use:   "metrics",
		Short: "fetch and filter the getter's Prometheus /metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			re, err := regexp.Compile(filter)
			if err != nil {
				return fmt.Errorf("bad --filter: %w", err)
			}
			if !watch {
				return fetchAndPrintMetrics(url, re, cmd.OutOrStdout())
			}
			for {
				if err := fetchAndPrintMetrics(url, re, cmd.OutOrStdout()); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
				}
				time.Sleep(2 * time.Second)
			}
		},
	}
	c.Flags().StringVar(&url, "url", "http://localhost:9100/metrics", "metrics endpoint URL")
	c.Flags().StringVar(&filter, "filter", "^harporis_", "regex applied to each metric line; default: harporis_*")
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
	scanner := bufio.NewScanner(resp.Body)
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
