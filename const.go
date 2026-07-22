package main

import "time"

const (
	AppName    = "Houston, We Have a Packet Loss"
	AppVersion = "0.1.0"

	DefaultDishAddress = "192.168.100.1:9200"
	DefaultDialTimeout = 5 * time.Second

	// Polling: how often we ask the dish for a fresh status snapshot.
	DefaultPollInterval = 1 * time.Second

	// History: how much recent data the web UI keeps so a freshly opened browser
	// tab can backfill its chart instead of starting empty.
	HistoryWindow = 45 * time.Minute

	// Web UI: the dashboard
	DefaultWebAddr = ":2101"
	RouteIndex     = "/"
	RouteEvents    = "/events"
	RouteLog       = "/log"
	RouteHistory   = "/history"

	RangeAll         = "all"
	MaxRangeDuration = 24 * time.Hour
	MaxHistoryPoints = 2000

	// Persistence: relative path to raw telemetry log
	// single JSON-Lines file (one sample per line).
	// Kept as one file so it's grep-, tail-, and cat-friendly.
	DefaultLogFile = "houston.jsonl"

	// Link-health heuristic: thresholds for degraded/lost signal. 
	// Used to approximate connection health from packet-drop rate
	//
	// Values are decimals representing percentage loss
	// 1 = 100%, 0.25 = 25%
	DropRateNoSignal = 1.0  // fully dropping -> no usable signal
	DropRateDegraded = 0.02 // more than 2% drop -> degraded but still up

	// Simplified link states produced by the heuristic above.
	LinkOnline     = "ONLINE"
	LinkDegraded   = "DEGRADED"
	LinkObstructed = "OBSTRUCTED"
	LinkNoSignal   = "NO SIGNAL"
	LinkOffline    = "OFFLINE"
)
