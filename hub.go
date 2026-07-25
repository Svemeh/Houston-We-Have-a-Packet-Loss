package main

import "sync"

// subscriberQueueDepth is how many samples a single browser can fall behind
// before we start dropping its updates rather than stalling every other client.
const subscriberQueueDepth = 16

// TelemetryHub fans each new sample out to every connected browser and keeps a
// short rolling history, so a tab that connects late has something to draw.
type TelemetryHub struct {
	mutex              sync.Mutex
	recentSamples      []TelemetrySample                 // ring buffer, chronological order
	maxRetainedSamples int                               // how many samples recentSamples holds
	subscribers        map[chan TelemetrySample]struct{} // one channel per connected browser
}

func NewTelemetryHub(maxRetainedSamples int) *TelemetryHub {
	return &TelemetryHub{
		recentSamples:      make([]TelemetrySample, 0, maxRetainedSamples),
		maxRetainedSamples: maxRetainedSamples,
		subscribers:        make(map[chan TelemetrySample]struct{}),
	}
}

// SeedHistory pre-fills the rolling history, normally from the log file at startup.
func (hub *TelemetryHub) SeedHistory(samples []TelemetrySample) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()
	if len(samples) > hub.maxRetainedSamples {
		samples = samples[len(samples)-hub.maxRetainedSamples:]
	}
	hub.recentSamples = append(hub.recentSamples[:0], samples...)
}

func (hub *TelemetryHub) PublishSample(sample TelemetrySample) {
	hub.mutex.Lock()
	hub.recentSamples = append(hub.recentSamples, sample)
	if len(hub.recentSamples) > hub.maxRetainedSamples {
		hub.recentSamples = hub.recentSamples[len(hub.recentSamples)-hub.maxRetainedSamples:]
	}

	currentSubscribers := make([]chan TelemetrySample, 0, len(hub.subscribers))
	for subscriber := range hub.subscribers {
		currentSubscribers = append(currentSubscribers, subscriber)
	}
	hub.mutex.Unlock()

	for _, subscriber := range currentSubscribers {
		select {
		case subscriber <- sample:
		default:
		}
	}
}

// Subscribe hands back a live stream, a copy of the history so far, and
// the function the caller must invoke to detach.
func (hub *TelemetryHub) Subscribe() (stream <-chan TelemetrySample, backfill []TelemetrySample, unsubscribe func()) {
	hub.mutex.Lock()
	defer hub.mutex.Unlock()

	subscriber := make(chan TelemetrySample, subscriberQueueDepth)
	hub.subscribers[subscriber] = struct{}{}

	snapshot := make([]TelemetrySample, len(hub.recentSamples))
	copy(snapshot, hub.recentSamples)

	return subscriber, snapshot, func() {
		hub.mutex.Lock()
		if _, stillSubscribed := hub.subscribers[subscriber]; stillSubscribed {
			delete(hub.subscribers, subscriber)
			close(subscriber)
		}
		hub.mutex.Unlock()
	}
}
