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

const (
	// readWholeFile tells scanLogTail not to seek — read from byte zero.
	readWholeFile = -1

	// Tail-size estimation. Records run ~250 bytes at DefaultPollInterval;
	// tailEstimateSlack covers fatter ones, and we never bother reading less
	// than minTailBytes.
	approxBytesPerSample = 250
	tailEstimateSlack    = 4
	minTailBytes         = 1 << 20
)

// historyResponse is what RouteHistory returns. BucketWidthMs tells the client how
// far apart adjacent points are so it can size its gap-detection threshold —
// without it a downsampled series renders as disconnected fragments.
type historyResponse struct {
	RequestedRange string            `json:"requested_range"`
	WindowStart    time.Time         `json:"window_start"`
	BucketWidthMs  int64             `json:"bucket_width_ms"`
	RawCount       int               `json:"raw_count"`  // samples read before downsampling
	KeptCount      int               `json:"kept_count"` // samples actually returned
	Samples        []TelemetrySample `json:"samples"`
}

func newHistoryHandler(logPath string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		requestedRange := strings.TrimSpace(request.URL.Query().Get("range"))

		// A zero oldestWanted means "read everything".
		var oldestWanted time.Time
		if requestedRange != "" && requestedRange != RangeAll {
			requestedSpan, err := time.ParseDuration(requestedRange)
			if err != nil || requestedSpan <= 0 {
				http.Error(response, "range must be a duration like 15m, or "+RangeAll, http.StatusBadRequest)
				return
			}
			if requestedSpan > MaxHistoryRange {
				requestedSpan = MaxHistoryRange
			}
			oldestWanted = time.Now().Add(-requestedSpan)
		}

		samples, err := LoadSamplesSince(logPath, oldestWanted)
		if err != nil {
			http.Error(response, "history unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}

		now := time.Now()
		windowStart := oldestWanted
		if windowStart.IsZero() {
			// "All" — the window begins at the oldest record we have.
			windowStart = now
			if len(samples) > 0 {
				windowStart = samples[0].Timestamp
			}
		}

		points, bucketWidth := DownsampleWorstCase(samples, windowStart, now, MaxHistoryPoints)

		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(response).Encode(historyResponse{
			RequestedRange: requestedRange,
			WindowStart:    windowStart,
			BucketWidthMs:  bucketWidth.Milliseconds(),
			RawCount:       len(samples),
			KeptCount:      len(points),
			Samples:        points,
		})
	}
}

// LoadSamplesSince returns every sample at or after oldestWanted, oldest first.
// A zero oldestWanted reads the whole file.
func LoadSamplesSince(logPath string, oldestWanted time.Time) ([]TelemetrySample, error) {
	samples, didSeek, err := scanLogTail(logPath, oldestWanted, estimateTailBytes(oldestWanted))
	if err != nil {
		return nil, err
	}
	// We guessed how far back to seek. If we started mid-file and the oldest
	// record we found is still newer than the cutoff, records we wanted sit
	// before our starting offset — the guess was too small, so re-read fully.
	if didSeek && len(samples) > 0 && samples[0].Timestamp.After(oldestWanted) {
		samples, _, err = scanLogTail(logPath, oldestWanted, readWholeFile)
		if err != nil {
			return nil, err
		}
	}
	return samples, nil
}

// estimateTailBytes guesses how many trailing bytes could hold the requested
// span, so a 2-minute request doesn't parse a month of history. LoadSamplesSince
// re-reads from the top if the estimate came up short.
func estimateTailBytes(oldestWanted time.Time) int64 {
	if oldestWanted.IsZero() {
		return readWholeFile
	}
	requestedSpan := time.Since(oldestWanted)
	if requestedSpan <= 0 {
		requestedSpan = time.Minute
	}
	estimatedBytes := int64(requestedSpan/DefaultPollInterval) * approxBytesPerSample * tailEstimateSlack
	if estimatedBytes < minTailBytes {
		estimatedBytes = minTailBytes
	}
	return estimatedBytes
}

// scanLogTail reads the last maxTailBytes of the log (or all of it when
// maxTailBytes is readWholeFile), returning in-range samples and whether it had
// to seek to get there.
func scanLogTail(logPath string, oldestWanted time.Time, maxTailBytes int64) (samples []TelemetrySample, didSeek bool, err error) {
	file, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, false, err
	}

	startOffset := int64(0)
	if maxTailBytes > 0 && fileInfo.Size() > maxTailBytes {
		startOffset = fileInfo.Size() - maxTailBytes
		didSeek = true
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return nil, false, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, scannerInitialBufferBytes), scannerMaxLineBytes)

	skipPartialFirstLine := didSeek // first line after a seek is probably a fragment
	for scanner.Scan() {
		if skipPartialFirstLine {
			skipPartialFirstLine = false
			continue
		}
		var sample TelemetrySample
		// Corrupted lines (a partial write from a crash, or the tail of a
		// record being appended right now) are skipped silently.
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			continue
		}
		if oldestWanted.IsZero() || !sample.Timestamp.Before(oldestWanted) {
			samples = append(samples, sample)
		}
	}
	return samples, didSeek, scanner.Err()
}

// DownsampleWorstCase collapses samples into at most maxPoints time buckets,
// keeping the worst reading in each, and reports the resulting bucket width.
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
func DownsampleWorstCase(samples []TelemetrySample, windowStart, windowEnd time.Time, maxPoints int) ([]TelemetrySample, time.Duration) {
	windowSpan := windowEnd.Sub(windowStart)
	if maxPoints <= 0 || len(samples) <= maxPoints || windowSpan <= 0 {
		return samples, DefaultPollInterval
	}
	bucketWidth := windowSpan / time.Duration(maxPoints)
	if bucketWidth <= 0 {
		return samples, DefaultPollInterval
	}

	downsampled := make([]TelemetrySample, 0, maxPoints+1)
	worstInBucket := TelemetrySample{}
	currentBucketIndex := int64(-1)

	for _, sample := range samples {
		bucketIndex := int64(sample.Timestamp.Sub(windowStart) / bucketWidth)
		if bucketIndex != currentBucketIndex {
			if currentBucketIndex >= 0 {
				downsampled = append(downsampled, worstInBucket)
			}
			worstInBucket, currentBucketIndex = sample, bucketIndex
			continue
		}
		worstInBucket = mergeWorstCase(worstInBucket, sample)
	}
	if currentBucketIndex >= 0 {
		downsampled = append(downsampled, worstInBucket)
	}
	return downsampled, bucketWidth
}

// mergeWorstCase folds candidate into worst, keeping the least flattering value
// of each field. The timestamp stays at the bucket's first sample so output is
// monotonic.
func mergeWorstCase(worst, candidate TelemetrySample) TelemetrySample {
	if candidate.LatencyMs > worst.LatencyMs {
		worst.LatencyMs = candidate.LatencyMs
	}
	if candidate.DownloadMbps > worst.DownloadMbps {
		worst.DownloadMbps = candidate.DownloadMbps
	}
	if candidate.UploadMbps > worst.UploadMbps {
		worst.UploadMbps = candidate.UploadMbps
	}
	if candidate.DropRateFraction > worst.DropRateFraction {
		worst.DropRateFraction = candidate.DropRateFraction
	}
	if candidate.ObstructionFraction > worst.ObstructionFraction {
		worst.ObstructionFraction = candidate.ObstructionFraction
	}
	worst.Obstructed = worst.Obstructed || candidate.Obstructed

	if linkStateSeverity(candidate.LinkState) > linkStateSeverity(worst.LinkState) {
		worst.LinkState = candidate.LinkState
	}
	// A bucket only reads OFFLINE if every poll in it failed — one bad poll
	// among good ones shouldn't blank the bucket, since the client drops
	// OFFLINE samples from the charts entirely.
	if worst.LinkState != LinkStateOffline {
		worst.PollError = ""
	}

	worst.UptimeSeconds = candidate.UptimeSeconds
	if candidate.HardwareVersion != "" {
		worst.HardwareVersion = candidate.HardwareVersion
	}
	if candidate.SoftwareVersion != "" {
		worst.SoftwareVersion = candidate.SoftwareVersion
	}
	return worst
}

// linkStateSeverity ranks link states for merging. OFFLINE sorts below every
// real reading so that any successful poll in a bucket wins.
func linkStateSeverity(linkState string) int {
	switch linkState {
	case LinkStateOnline:
		return 1
	case LinkStateDegraded:
		return 2
	case LinkStateObstructed:
		return 3
	case LinkStateNoSignal:
		return 4
	default:
		return 0
	}
}
