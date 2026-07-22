package main

import "time"

type Sample struct {
	Timestamp       time.Time `json:"timestamp"`
	Link            string    `json:"link"`
	LatencyMs       float64   `json:"latency_ms"`
	DownlinkMbps    float64   `json:"downlink_mbps"`
	UplinkMbps      float64   `json:"uplink_mbps"`
	DropRate        float64   `json:"drop_rate"` // percentage in [0,1]
	Obstructed      bool      `json:"obstructed"`
	ObstructionFraction float64 `json:"obstruction_fraction"`
	UptimeSeconds   uint64    `json:"uptime_seconds"`
	HardwareVersion string    `json:"hardware_version"`
	SoftwareVersion string    `json:"software_version"`
	// Err is set when a poll failed empty otherwise.
	Err string `json:"error,omitempty"`
}
