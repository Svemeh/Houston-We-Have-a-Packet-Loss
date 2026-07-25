package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

const (
	// maxStartupTailBytes caps how much of the log we re-read when seeding the
	// hub. 5 MB is comfortably more than one hour of samples (~250 bytes each
	// × 3600 ≈ 900 KB), with headroom for larger records.
	//
	// TODO: make this configurable instead of hardcoding it.
	maxStartupTailBytes = 5 << 20

	// Scanner sizing: start with a 64 KB line buffer, refuse anything past 1 MB
	// so a corrupt file can't be read into memory as one enormous "line".
	scannerInitialBufferBytes = 64 * 1024
	scannerMaxLineBytes       = 1 << 20
)

type LogFile struct {
	path           string
	mutex          sync.Mutex // serializes Append; concurrent HTTP reads are OS-safe
	file           *os.File
	bufferedWriter *bufio.Writer
}

func OpenLogFile(path string) (*LogFile, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &LogFile{path: path, file: file, bufferedWriter: bufio.NewWriter(file)}, nil
}

// Append writes one sample to the file as a single JSON line and flushes to disk.
func (logFile *LogFile) Append(sample TelemetrySample) error {
	jsonLine, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	logFile.mutex.Lock()
	defer logFile.mutex.Unlock()
	if _, err := logFile.bufferedWriter.Write(jsonLine); err != nil {
		return err
	}
	if err := logFile.bufferedWriter.WriteByte('\n'); err != nil {
		return err
	}
	return logFile.bufferedWriter.Flush()
}

func (logFile *LogFile) Close() error {
	logFile.mutex.Lock()
	defer logFile.mutex.Unlock()
	if err := logFile.bufferedWriter.Flush(); err != nil {
		return err
	}
	return logFile.file.Close()
}

// LoadRecentSamples returns samples at or after oldestWanted, oldest first.
// It only ever looks at the last maxStartupTailBytes of the file, so a very old
// cutoff can come back short — fine for seeding the hub at startup, where the
// window is minutes. Use LoadSamplesSince when completeness matters.
func LoadRecentSamples(path string, oldestWanted time.Time) ([]TelemetrySample, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}

	fileSize := fileInfo.Size()
	startOffset := int64(0)
	skipPartialFirstLine := false
	if fileSize > maxStartupTailBytes {
		startOffset = fileSize - maxStartupTailBytes
		skipPartialFirstLine = true // may be a truncated fragment
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, scannerInitialBufferBytes), scannerMaxLineBytes)

	var samples []TelemetrySample
	for scanner.Scan() {
		if skipPartialFirstLine {
			skipPartialFirstLine = false
			continue
		}
		var sample TelemetrySample
		// Corrupted lines (e.g. a partial write from a crash) are skipped
		// silently — one bad line shouldn't discard the whole tail.
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil {
			continue
		}
		if !sample.Timestamp.Before(oldestWanted) {
			samples = append(samples, sample)
		}
	}
	return samples, scanner.Err()
}
