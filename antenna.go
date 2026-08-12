package main

import (
	"context"
	"fmt"
	"time"

	device "github.com/starlink-community/starlink-grpc-go/pkg/spacex.com/api/device"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TelemetryCollector is anything that can produce one sample on demand: the
// live dish, or a fake standing in for it.
type TelemetryCollector interface {
	Collect(ctx context.Context) (TelemetrySample, error)
	Close() error
}

// newCollector builds the telemetry source, either: ( live dish / fake dish )
func newCollector(useFake bool, dishAddress string) (TelemetryCollector, error) {
	if useFake { return NewFakeCollector(), nil }
	return NewStarlinkCollector(dishAddress)
}

type StarlinkCollector struct {
	connection   *grpc.ClientConn
	deviceClient device.DeviceClient
}

func NewStarlinkCollector(dishAddress string) (*StarlinkCollector, error) {
	connection, err := grpc.NewClient(dishAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}
	return &StarlinkCollector{connection: connection, deviceClient: device.NewDeviceClient(connection)}, nil
}

func (collector *StarlinkCollector) Collect(ctx context.Context) (TelemetrySample, error) {
	response, err := collector.deviceClient.Handle(ctx, &device.Request{
		Request: &device.Request_GetStatus{GetStatus: &device.GetStatusRequest{}},
	})
	if err != nil {
		return TelemetrySample{}, fmt.Errorf("GetStatus: %w", err)
	}
	dishStatus := response.GetDishGetStatus()
	if dishStatus == nil {
		return TelemetrySample{}, fmt.Errorf("response contained no dish status")
	}
	obstructionStats := dishStatus.GetObstructionStats()
	isObstructed := obstructionStats.GetCurrentlyObstructed()
	dropRateFraction := float64(dishStatus.GetPopPingDropRate())

	return TelemetrySample{
		Timestamp:           time.Now(),
		LinkState:           deriveLinkState(dropRateFraction, isObstructed),
		LatencyMs:           float64(dishStatus.GetPopPingLatencyMs()),
		DownloadMbps:        float64(dishStatus.GetDownlinkThroughputBps()) / 1e6, // bps -> Mbps
		UploadMbps:          float64(dishStatus.GetUplinkThroughputBps()) / 1e6,   // bps -> Mbps
		DropRateFraction:    dropRateFraction,
		Obstructed:          isObstructed,
		ObstructionFraction: float64(obstructionStats.GetFractionObstructed()),
		UptimeSeconds:       dishStatus.GetDeviceState().GetUptimeS(),
		HardwareVersion:     dishStatus.GetDeviceInfo().GetHardwareVersion(),
		SoftwareVersion:     dishStatus.GetDeviceInfo().GetSoftwareVersion(),
	}, nil
}

func (collector *StarlinkCollector) Close() error { return collector.connection.Close() }

func deriveLinkState(dropRateFraction float64, isObstructed bool) string {
	switch {
	case isObstructed:
		return LinkStateObstructed
	case dropRateFraction >= DropRateNoSignalThreshold:
		return LinkStateNoSignal
	case dropRateFraction > DropRateDegradedThreshold:
		return LinkStateDegraded
	default:
		return LinkStateOnline
	}
}
