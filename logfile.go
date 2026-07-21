package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

type LogFile struct {
	path string
	mu   sync.Mutex // serializes Append; concurrent HTTP reads are OS-safe
	f    *os.File
	w    *bufio.Writer
}

func OpenLogFile(path string) (*LogFile, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &LogFile{path: path, f: f, w: bufio.NewWriter(f)}, nil
}

// Append writes one Sample to the file as a single JSON line and flushes to disk.
func (l *LogFile) Append(s Sample) error {
	line, err := json.Marshal(s)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(line); err != nil {
		return err
	}
	if err := l.w.WriteByte('\n'); err != nil {
		return err
	}
	return l.w.Flush()
}

func (l *LogFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.w.Flush(); err != nil {
		return err
	}
	return l.f.Close()
}

func LoadTail(path string, cutoff time.Time) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// 5 MB is comfortably more than one hour of samples (~250 bytes each
	// × 3600 ≈ 900 KB), with headroom for larger records.
	// probs shouldnt be hardcoded though!!
	const tailBudget = 5 << 20
	size := stat.Size()
	start := int64(0)
	skipFirstLine := false
	if size > tailBudget {
		start = size - tailBudget
		skipFirstLine = true // may be a truncated fragment
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)

	var samples []Sample
	for sc.Scan() {
		if skipFirstLine {
			skipFirstLine = false
			continue
		}
		var s Sample
		// Corrupted lines (e.g. a partial write from a crash) are skipped
		// silently — one bad line shouldn't discard the whole tail.
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			continue
		}
		if !s.Timestamp.Before(cutoff) {
			samples = append(samples, s)
		}
	}
	return samples, sc.Err()
}
