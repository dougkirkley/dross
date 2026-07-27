package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/holocron/internal/agent"
)

// compactStatsPath is where compaction observations are appended (JSONL), so the
// repeat-rate of tool-result hashes can be measured across sessions.
func compactStatsPath() (string, error) {
	sp, err := storePath()
	if err != nil {
		return "", err
	}
	return sp + "/../compact-stats.jsonl", nil
}

// compactLine is one persisted compaction record.
type compactLine struct {
	Hash string `json:"hash"`
	Tool string `json:"tool"`
	Raw  int    `json:"raw"`
	Out  int    `json:"out"`
}

// newCompactRecorder returns a concurrency-safe CompactStatsFunc that appends
// JSONL to path. On any file error it silently drops the record — measurement
// must never break a run.
func newCompactRecorder(path string) agent.CompactStatsFunc {
	var mu sync.Mutex
	return func(r agent.CompactRecord) {
		mu.Lock()
		defer mu.Unlock()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		b, err := json.Marshal(compactLine{Hash: r.Hash, Tool: r.Tool, Raw: r.RawBytes, Out: r.OutBytes})
		if err != nil {
			return
		}
		f.Write(append(b, '\n'))
	}
}

func newCompactStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "compact-stats",
		Short: "Report compaction repeat-rate (would a cross-session summary cache pay off?)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := compactStatsPath()
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Fprintln(cmd.OutOrStdout(), "no compactions recorded yet")
					return nil
				}
				return err
			}
			defer f.Close()

			type agg struct {
				count    int // times this exact content was compacted
				rawBytes int // raw size (constant per hash)
				modelOut int // compacted size we forwarded
			}
			byHash := map[string]*agg{}
			total := 0
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
			for sc.Scan() {
				var l compactLine
				if json.Unmarshal(sc.Bytes(), &l) != nil {
					continue
				}
				total++
				a := byHash[l.Hash]
				if a == nil {
					a = &agg{rawBytes: l.Raw, modelOut: l.Out}
					byHash[l.Hash] = a
				}
				a.count++
			}
			if err := sc.Err(); err != nil {
				return err
			}

			unique := len(byHash)
			var repeatedHashes, redundantCalls, cacheableRawBytes int
			for _, a := range byHash {
				if a.count > 1 {
					repeatedHashes++
					redundantCalls += a.count - 1                   // model calls a cache would have avoided
					cacheableRawBytes += a.rawBytes * (a.count - 1) // raw bytes re-summarized needlessly
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "compactions:      %d\n", total)
			fmt.Fprintf(out, "unique contents:  %d\n", unique)
			fmt.Fprintf(out, "repeated contents: %d\n", repeatedHashes)
			fmt.Fprintf(out, "redundant calls:  %d  (a content-hash cache would have served these)\n", redundantCalls)
			fmt.Fprintf(out, "re-summarized:    %d bytes\n", cacheableRawBytes)
			hitRate := 0.0
			if total > 0 {
				hitRate = float64(redundantCalls) / float64(total) * 100
			}
			fmt.Fprintf(out, "cache hit rate:   %.1f%%\n", hitRate)
			switch {
			case total < 20:
				fmt.Fprintln(out, "\nverdict: not enough data yet — keep running.")
			case hitRate < 15:
				fmt.Fprintln(out, "\nverdict: low reuse — a persistent cache is not worth it.")
			default:
				fmt.Fprintln(out, "\nverdict: meaningful reuse — a content-hash summary cache would pay off.")
			}
			return nil
		},
	}
}
