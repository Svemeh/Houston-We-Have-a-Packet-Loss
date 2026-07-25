package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// shutdownGracePeriod is how long in-flight requests get to finish after Ctrl+C.
const shutdownGracePeriod = 3 * time.Second

//go:embed static/*
var embeddedStaticFS embed.FS

func serveDashboard(ctx context.Context, listenAddress string, hub *TelemetryHub, logPath string) error {
	staticRoot, err := fs.Sub(embeddedStaticFS, "static")
	if err != nil {
		return err
	}

	router := http.NewServeMux()
	router.Handle(RouteIndex, http.FileServer(http.FS(staticRoot)))
	router.HandleFunc(RouteEvents, newSampleStreamHandler(hub))
	router.HandleFunc(RouteLog, newRawLogHandler(logPath))
	router.HandleFunc(RouteHistory, newHistoryHandler(logPath))

	server := &http.Server{Addr: listenAddress, Handler: router}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownGracePeriod)
		defer cancelShutdown()
		_ = server.Shutdown(shutdownCtx)
	}()

	displayAddress := listenAddress
	if strings.HasPrefix(displayAddress, ":") {
		displayAddress = "localhost" + displayAddress
	}
	log.Printf("%s — dashboard live at http://%s (Ctrl+C to stop)", AppName, displayAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// newSampleStreamHandler serves RouteEvents: one Server-Sent Events stream per
// browser, opening with a backfill of the hub's recent history.
func newSampleStreamHandler(hub *TelemetryHub) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		flusher, canFlush := response.(http.Flusher)
		if !canFlush {
			http.Error(response, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		response.Header().Set("Cache-Control", "no-cache")
		response.Header().Set("Connection", "keep-alive")

		sampleStream, backfill, unsubscribe := hub.Subscribe()
		defer unsubscribe()

		if len(backfill) > 0 {
			payload, err := json.Marshal(backfill)
			if err == nil {
				fmt.Fprintf(response, "event: backfill\ndata: %s\n\n", payload)
				flusher.Flush()
			}
		}

		for {
			select {
			case <-request.Context().Done():
				return
			case sample, streamOpen := <-sampleStream:
				if !streamOpen {
					return
				}
				payload, err := json.Marshal(sample)
				if err != nil {
					continue
				}
				fmt.Fprintf(response, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

// newRawLogHandler serves RouteLog: the JSONL file, streamed back verbatim.
func newRawLogHandler(logPath string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")

		logFile, err := os.Open(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(response, "# no telemetry recorded yet")
				return
			}
			http.Error(response, "log unavailable: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer logFile.Close()
		_, _ = io.Copy(response, logFile)
	}
}
