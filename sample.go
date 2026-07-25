package main

import "time"

// TelemetrySample is one poll of the dish: the values it reported, or the
// error that stopped us from reading them.
//
// The JSON tags are the on-disk log format (houston.jsonl) and
// the wire format the dashboard reads.
type TelemetrySample struct {
	Timestamp           time.Time `json:"timestamp"`
	LinkState           string    `json:"link_state"`
	LatencyMs           float64   `json:"latency_ms"`
	DownlinkMbps        float64   `json:"downlink_mbps"`
	UplinkMbps          float64   `json:"uplink_mbps"`
	DropRateFraction    float64   `json:"drop_rate_fraction"` // fraction in [0,1]
	Obstructed          bool      `json:"obstructed"`
	ObstructionFraction float64   `json:"obstruction_fraction"`
	UptimeSeconds       uint64    `json:"uptime_seconds"`
	HardwareVersion     string    `json:"hardware_version"`
	SoftwareVersion     string    `json:"software_version"`
	// PollError is set when a poll failed, and empty otherwise.
	PollError string `json:"poll_error,omitempty"`
}
