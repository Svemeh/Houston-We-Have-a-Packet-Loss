package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// historyResponse is what RouteHistory returns. BucketMs tells the client how
// far apart adjacent points are so it can size its gap-detection threshold —
// without it a decimated series renders as disconnected fragments.
type historyResponse struct {
	Range    string    `json:"range"`
	Start    time.Time `json:"start"`
	BucketMs int64     `json:"bucket_ms"`
	Raw      int       `json:"raw"`  // samples read before decimation
	Kept     int       `json:"kept"` // samples actually returned
	Samples  []Sample  `json:"samples"`
}

func serveHistory(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("range"))

		// A zero cutoff means "read everything".
		var cutoff time.Time
		if q != "" && q != RangeAll {
			d, err := time.ParseDuration(q)
			if err != nil || d <= 0 {
				http.Error(w, "range must be a duration like 15m, or "+RangeAll, http.StatusBadRequest)
				return
			}
			if d > MaxRangeDuration {
				d = MaxRangeDuration
			}
			cutoff = time.Now().Add(-d)
		}

		samples, err := LoadRange(path, cutoff)
		if err != nil {
			http.Error(w, "history unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}

		now := time.Now()
		start := cutoff
		if start.IsZero() {
			// "All" — the window begins at the oldest record we have.
			start = now
			if len(samples) > 0 {
				start = samples[0].Timestamp
			}
		}

		kept, bucket := Decimate(samples, start, now, MaxHistoryPoints)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(historyResponse{
			Range:    q,
			Start:    start,
			BucketMs: bucket.Milliseconds(),
			Raw:      len(samples),
			Kept:     len(kept),
			Samples:  kept,
		})
	}
}

// LoadRange returns every sample at or after cutoff, oldest first. A zero
// cutoff reads the whole file.
func LoadRange(path string, cutoff time.Time) ([]Sample, error) {
	samples, seeked, err := scanFrom(path, cutoff, tailBudgetFor(cutoff))
	if err != nil {
		return nil, err
	}
	// We guessed how far back to seek. If we started mid-file and the oldest
	// record we found is still newer than the cutoff, records we wanted sit
	// before our starting offset — the guess was too small, so re-read fully.
	if seeked && len(samples) > 0 && samples[0].Timestamp.After(cutoff) {
		samples, _, err = scanFrom(path, cutoff, -1)
		if err != nil {
			return nil, err
		}
	}
	return samples, nil
}

// tailBudgetFor estimates how many trailing bytes could hold the requested
// span, so a 2-minute request doesn't parse a month of history. Records run
// ~250 bytes at DefaultPollInterval; the 4x slack covers fatter ones, and
// LoadRange re-reads from the top if the estimate still came up short.
func tailBudgetFor(cutoff time.Time) int64 {
	if cutoff.IsZero() {
		return -1 // whole file
	}
	d := time.Since(cutoff)
	if d <= 0 {
		d = time.Minute
	}
	n := int64(d/DefaultPollInterval) * 250 * 4
	if n < 1<<20 {
		n = 1 << 20
	}
	return n
}

// scanFrom reads the last `budget` bytes of the log (or all of it when budget
// is negative), returning in-range samples and whether it had to seek.
func scanFrom(path string, cutoff time.Time, budget int64) (samples []Sample, seeked bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, false, err
	}

	start := int64(0)
	if budget > 0 && stat.Size() > budget {
		start = stat.Size() - budget
		seeked = true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	skipFirst := seeked // first line after a seek is probably a fragment
	for sc.Scan() {
		if skipFirst {
			skipFirst = false
			continue
		}
		var s Sample
		// Corrupted lines (a partial write from a crash, or the tail of a
		// record being appended right now) are skipped silently.
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			continue
		}
		if cutoff.IsZero() || !s.Timestamp.Before(cutoff) {
			samples = append(samples, s)
		}
	}
	return samples, seeked, sc.Err()
}

// Decimate collapses samples into at most maxPoints time buckets, keeping the
// worst reading in each, and reports the resulting bucket width.
//
// Buckets are cut by time rather than by index so that an outage stays an
// outage: a stretch with no samples produces no points, and the client's gap
// detection still renders it as blank space.
//
// Worst-case rather than mean is deliberate. Averaging a three-second dropout
// into a minute-wide bucket hides exactly the event this dashboard exists to
// show. The cost is that at long ranges you are reading peaks, not readings —
// a 24h chart showing 400 ms is saying "something touched 400 ms in that
// bucket", not "latency was 400 ms".
func Decimate(samples []Sample, start, end time.Time, maxPoints int) ([]Sample, time.Duration) {
	span := end.Sub(start)
	if maxPoints <= 0 || len(samples) <= maxPoints || span <= 0 {
		return samples, DefaultPollInterval
	}
	bucket := span / time.Duration(maxPoints)
	if bucket <= 0 {
		return samples, DefaultPollInterval
	}

	out := make([]Sample, 0, maxPoints+1)
	cur := Sample{}
	curIdx := int64(-1)

	for _, s := range samples {
		idx := int64(s.Timestamp.Sub(start) / bucket)
		if idx != curIdx {
			if curIdx >= 0 {
				out = append(out, cur)
			}
			cur, curIdx = s, idx
			continue
		}
		cur = mergeWorst(cur, s)
	}
	if curIdx >= 0 {
		out = append(out, cur)
	}
	return out, bucket
}

// mergeWorst folds b into a, keeping the least flattering value of each field.
// The timestamp stays at the bucket's first sample so output is monotonic.
func mergeWorst(a, b Sample) Sample {
	if b.LatencyMs > a.LatencyMs {
		a.LatencyMs = b.LatencyMs
	}
	if b.DownlinkMbps > a.DownlinkMbps {
		a.DownlinkMbps = b.DownlinkMbps
	}
	if b.UplinkMbps > a.UplinkMbps {
		a.UplinkMbps = b.UplinkMbps
	}
	if b.DropRate > a.DropRate {
		a.DropRate = b.DropRate
	}
	if b.ObstructionFraction > a.ObstructionFraction {
		a.ObstructionFraction = b.ObstructionFraction
	}
	a.Obstructed = a.Obstructed || b.Obstructed

	if linkSeverity(b.Link) > linkSeverity(a.Link) {
		a.Link = b.Link
	}
	// A bucket only reads OFFLINE if every poll in it failed — one bad poll
	// among good ones shouldn't blank the bucket, since the client drops
	// OFFLINE samples from the charts entirely.
	if a.Link != LinkOffline {
		a.Err = ""
	}

	a.UptimeSeconds = b.UptimeSeconds
	if b.HardwareVersion != "" {
		a.HardwareVersion = b.HardwareVersion
	}
	if b.SoftwareVersion != "" {
		a.SoftwareVersion = b.SoftwareVersion
	}
	return a
}

// linkSeverity ranks link states for merging. OFFLINE sorts below every real
// reading so that any successful poll in a bucket wins.
func linkSeverity(link string) int {
	switch link {
	case LinkOnline:
		return 1
	case LinkDegraded:
		return 2
	case LinkObstructed:
		return 3
	case LinkNoSignal:
		return 4
	default:
		return 0
	}
}
