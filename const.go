package main

import "time"

const (
	AppName    = "Houston, We Have a Packet Loss"
	AppVersion = "0.1.0"

	DefaultDishAddress    = "192.168.100.1:9200"
	DefaultRequestTimeout = 5 * time.Second

	// Polling: how often we ask the dish for a fresh status snapshot.
	DefaultPollInterval = 1 * time.Second

	// History: how much recent data the web UI keeps so a freshly opened browser
	// tab can backfill its chart instead of starting empty.
	BackfillWindow = 45 * time.Minute

	// Web UI: the dashboard
	DefaultWebAddress = ":2101"
	RouteIndex        = "/"
	RouteEvents       = "/events"
	RouteLog          = "/log"
	RouteHistory      = "/history"

	RangeAll         = "all"
	MaxHistoryRange  = 24 * time.Hour
	MaxHistoryPoints = 2000

	// Persistence: relative path to raw telemetry log
	// single JSON-Lines file (one sample per line).
	// ~250 bytes per sample, 3600 samples per hour = 900KB/hour
	// Kept as one file so it's grep-, tail-, and cat-friendly.
	DefaultLogFilePath = "houston.jsonl"

	// LogRetention: samples older than this are pruned from the log.
	// The log files grows at roughly ~900KB/hour.
	// so a rough estimate of max file size is: 900KB * LogRetention + LogPruneInterval
	LogRetention = 72 * time.Hour

	// LogPruneInterval: how often the pruner runs.
	LogPruneInterval = 1 * time.Hour

	// Link-health heuristic: thresholds for degraded/lost signal.
	// Used to approximate connection health from packet-drop rate
	//
	// Values are decimals representing percentage loss
	// 1 = 100%, 0.25 = 25%
	DropRateNoSignalThreshold = 1.0  // fully dropping -> no usable signal
	DropRateDegradedThreshold = 0.02 // more than 2% drop -> degraded but still up

	// Simplified link states produced by the heuristic above.
	LinkStateOnline     = "ONLINE"
	LinkStateDegraded   = "DEGRADED"
	LinkStateObstructed = "OBSTRUCTED"
	LinkStateNoSignal   = "NO SIGNAL"
	LinkStateOffline    = "OFFLINE"
)
