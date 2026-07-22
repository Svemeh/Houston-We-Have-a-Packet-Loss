package main

import (
	"context"
	"fmt"
	"time"

	device "github.com/starlink-community/starlink-grpc-go/pkg/spacex.com/api/device"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Collector interface {
	Collect(ctx context.Context) (Sample, error)
	Close() error
}

type StarlinkCollector struct {
	conn   *grpc.ClientConn
	client device.DeviceClient
}

func NewStarlinkCollector(addr string) (*StarlinkCollector, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return &StarlinkCollector{conn: conn, client: device.NewDeviceClient(conn)}, nil
}

func (c *StarlinkCollector) Collect(ctx context.Context) (Sample, error) {
	resp, err := c.client.Handle(ctx, &device.Request{
		Request: &device.Request_GetStatus{GetStatus: &device.GetStatusRequest{}},
	})
	if err != nil {
		return Sample{}, fmt.Errorf("GetStatus: %w", err)
	}
	s := resp.GetDishGetStatus()
	if s == nil {
		return Sample{}, fmt.Errorf("response contained no dish status")
	}
	obs := s.GetObstructionStats()
	obstructed := obs.GetCurrentlyObstructed()
	drop := float64(s.GetPopPingDropRate())

	return Sample{
		Timestamp:       time.Now(),
		Link:            deriveLink(drop, obstructed),
		LatencyMs:       float64(s.GetPopPingLatencyMs()),
		DownlinkMbps:    float64(s.GetDownlinkThroughputBps()) / 1e6, // bps -> Mbps
		UplinkMbps:      float64(s.GetUplinkThroughputBps()) / 1e6,
		DropRate:        drop,
		Obstructed:      obstructed,
		ObstructionFraction: float64(obs.GetFractionObstructed()),
		UptimeSeconds:   s.GetDeviceState().GetUptimeS(),
		HardwareVersion: s.GetDeviceInfo().GetHardwareVersion(),
		SoftwareVersion: s.GetDeviceInfo().GetSoftwareVersion(),
	}, nil
}

func (c *StarlinkCollector) Close() error { return c.conn.Close() }

func deriveLink(dropRate float64, obstructed bool) string {
	switch {
	case obstructed:
		return LinkObstructed
	case dropRate >= DropRateNoSignal:
		return LinkNoSignal
	case dropRate > DropRateDegraded:
		return LinkDegraded
	default:
		return LinkOnline
	}
}
