package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nano-step/nano-brain-observe/internal/record"
)

const defaultDir = "~/.nano-brain-observe/logs"

// parseSince parses a Go duration, or a whole number of days such as "7d".
func parseSince(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

type counts struct {
	calls, errors, empty int
	latencies            []int64
}

type summary struct {
	files, records, malformed int
	tools, clients            map[string]*counts
}

// summarize reads every calls-*.jsonl in dir and aggregates the mcp_tool
// records at or after since. Malformed lines are counted, not reported.
func summarize(dir string, since time.Time) (summary, error) {
	s := summary{tools: map[string]*counts{}, clients: map[string]*counts{}}
	paths, err := filepath.Glob(filepath.Join(dir, "calls-*.jsonl"))
	if err != nil {
		return s, err
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return s, err
		}
		s.files++
		err = s.scan(f, since)
		f.Close()
		if err != nil {
			return s, fmt.Errorf("read %s: %w", p, err)
		}
	}
	return s, nil
}

func (s *summary) scan(r io.Reader, since time.Time) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var rec record.Record
			if json.Unmarshal(line, &rec) != nil {
				s.malformed++
			} else if rec.Kind == record.KindMCPTool && !rec.Time.Before(since) {
				s.records++
				add(s.tools, rec.Tool, rec)
				add(s.clients, rec.Client, rec)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func add(m map[string]*counts, key string, rec record.Record) {
	if key == "" {
		key = "(unknown)"
	}
	c := m[key]
	if c == nil {
		c = &counts{}
		m[key] = c
	}
	c.calls++
	switch rec.Status {
	case record.StatusError:
		c.errors++
	case record.StatusEmpty:
		c.empty++
	}
	c.latencies = append(c.latencies, rec.LatencyMs)
}

// percentile returns the nearest-rank p-th percentile of sorted values.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100
	return sorted[max(rank, 1)-1]
}

func sortedKeys(m map[string]*counts) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := m[keys[i]].calls, m[keys[j]].calls; a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	return keys
}

func (s summary) print(w io.Writer, window string) {
	fmt.Fprintf(w, "window %s, %d files, %d records, %d malformed lines\n\n", window, s.files, s.records, s.malformed)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tCALLS\tERROR\tEMPTY\tP50 MS\tP95 MS\tMAX MS")
	for _, k := range sortedKeys(s.tools) {
		c := s.tools[k]
		sort.Slice(c.latencies, func(i, j int) bool { return c.latencies[i] < c.latencies[j] })
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\n", k, c.calls, c.errors, c.empty,
			percentile(c.latencies, 50), percentile(c.latencies, 95), c.latencies[len(c.latencies)-1])
	}
	tw.Flush()

	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CLIENT\tCALLS\tERROR")
	for _, k := range sortedKeys(s.clients) {
		fmt.Fprintf(tw, "%s\t%d\t%d\n", k, s.clients[k].calls, s.clients[k].errors)
	}
	tw.Flush()
}

// runStats implements the "stats" subcommand.
func runStats(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	dirFlag := fs.String("dir", envOr("NANO_BRAIN_OBSERVE_DIR", defaultDir), "directory of the JSONL call logs")
	sinceFlag := fs.String("since", "7d", "window: a Go duration or days such as 7d")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	window, err := parseSince(*sinceFlag)
	if err != nil {
		return err
	}
	dir, err := expandHome(*dirFlag)
	if err != nil {
		return err
	}
	s, err := summarize(dir, time.Now().Add(-window))
	if err != nil {
		return err
	}
	s.print(out, *sinceFlag)
	return nil
}
