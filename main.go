package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"
)

func main() {
	addr := flag.String("addr", DefaultDishAddress, "Starlink dish gRPC address")
	webAddr := flag.String("web", DefaultWebAddr, "dashboard listen address")
	logPath := flag.String("log", DefaultLogFile, "telemetry log file (JSONL)")
	check := flag.Bool("oneshot", false, "run a one-shot connectivity check and exit")
	timeout := flag.Duration("timeout", DefaultDialTimeout, "per-request timeout")
	flag.Parse()

	collector, err := NewStarlinkCollector(*addr)
	if err != nil {
	    fmt.Fprintf(os.Stderr, "✗ %v\n", err)
	    os.Exit(1)
	}

	defer collector.Close()

	// One-shot connectivity test — bypasses hub/logfile machinery.
	if *check {
		if err := runCheck(collector, *timeout); err != nil {
			fmt.Fprintf(os.Stderr, "✗ could not reach Starlink dish at %s: %v\n", *addr, err)
			os.Exit(1)
		}
		return
	}

	logFile, err := OpenLogFile(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ opening log file %q: %v\n", *logPath, err)
		os.Exit(1)
	}
	defer logFile.Close()

	capacity := int(HistoryWindow / DefaultPollInterval)
	hub := NewHub(capacity)

	initial, err := LoadTail(*logPath, time.Now().Add(-HistoryWindow))
	if err != nil {
		log.Printf("warning: could not load prior telemetry: %v", err)
	} else if len(initial) > 0 {
		hub.SetInitial(initial)
		log.Printf("loaded %d prior samples from %s", len(initial), *logPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	go poll(ctx, collector, hub, logFile)

	if err := serve(ctx, *webAddr, hub, *logPath); err != nil {
		fmt.Fprintf(os.Stderr, "✗ server error: %v\n", err)
		os.Exit(1)
	}
}

func poll(ctx context.Context, c Collector, hub *Hub, lf *LogFile) {
	ticker := time.NewTicker(DefaultPollInterval)
	defer ticker.Stop()

	tick := func() {
		pctx, cancel := context.WithTimeout(ctx, DefaultDialTimeout)
		defer cancel()
		s, err := c.Collect(pctx)
		if err != nil {
			s = Sample{Timestamp: time.Now(), Link: LinkOffline, Err: err.Error()}
		}
		hub.Publish(s)
		if err := lf.Append(s); err != nil {
			log.Printf("log write failed: %v", err)
		}
	}

	tick() // one immediate poll so /events isn't blank on first connect
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// runCheck performs a single poll and prints a human-readable summary.
func runCheck(c Collector, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s, err := c.Collect(ctx)
	if err != nil {
		return err
	}

	fmt.Println("✓ Connected to Starlink dish (received live telemetry)")
	fmt.Printf("  link:      %s\n", s.Link)
	fmt.Printf("  latency:   %.1f ms\n", s.LatencyMs)
	fmt.Printf("  drop rate: %.1f%%\n", s.DropRate*100)
	fmt.Printf("  hardware:  %s\n", s.HardwareVersion)
	fmt.Printf("  software:  %s\n", s.SoftwareVersion)
	fmt.Printf("  uptime:    %s\n", time.Duration(s.UptimeSeconds)*time.Second)
	return nil
}
