package agent

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

const (
	// blockProfileRate samples one goroutine-blocking event per this
	// many nanoseconds spent blocked, so the block profile captures
	// real contention while trivial waits stay mostly unsampled.
	blockProfileRate = 10000

	// mutexProfileFraction reports one in this many contended mutex events.
	// Contention is rare, so reporting every event stays cheap.
	mutexProfileFraction = 1
)

func newPprofServer(addr string, logger *slog.Logger) (*httpServer, error) {
	const name = "pprof"

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %q: %w", addr, err)
	}
	// The block and mutex profilers are off by default.
	runtime.SetBlockProfileRate(blockProfileRate)
	runtime.SetMutexProfileFraction(mutexProfileFraction)

	mux := http.NewServeMux()

	// Index looks a profile up by name, so goroutineleak and every
	// other predefined one are served without a dedicated handler.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/memory", memoryStatsHandler)

	// Do not set WriteTimeout, because a CPU or trace profile legitimately
	// streams for its whole duration (/debug/pprof/profile?seconds=N),
	// which a shorter write deadline would cut off.
	// IdleTimeout still reaps idle keep-alive connections.
	return &httpServer{
		srv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: serverReadHeaderTimeout,
			ReadTimeout:       serverReadTimeout,
			IdleTimeout:       serverIdleTimeout,
		},
		ln:   ln,
		log:  logger.With(slog.String("component", name)),
		name: name,
	}, nil
}

func memoryStatsHandler(w http.ResponseWriter, _ *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// LastGC is zero until the first collection finishes.
	var lastGC time.Time
	if ms.LastGC > 0 && ms.LastGC <= math.MaxInt64 {
		lastGC = time.Unix(0, int64(ms.LastGC)).UTC()
	}
	report := struct {
		Sys           uint64    `json:"sysBytes"`
		HeapAlloc     uint64    `json:"heapAllocBytes"`
		HeapInuse     uint64    `json:"heapInuseBytes"`
		HeapIdle      uint64    `json:"heapIdleBytes"`
		HeapObjects   uint64    `json:"heapObjects"`
		StackInuse    uint64    `json:"stackInuseBytes"`
		NextGC        uint64    `json:"nextGCBytes"`
		GCCycles      uint32    `json:"gcCycles"`
		LastGC        time.Time `json:"lastGC,omitzero"`
		GCCPUFraction float64   `json:"gcCPUFraction"`
		Goroutines    int       `json:"goroutines"`
	}{
		Sys:           ms.Sys,
		HeapAlloc:     ms.HeapAlloc,
		HeapInuse:     ms.HeapInuse,
		HeapIdle:      ms.HeapIdle - ms.HeapReleased,
		HeapObjects:   ms.HeapObjects,
		StackInuse:    ms.StackInuse,
		NextGC:        ms.NextGC,
		GCCycles:      ms.NumGC,
		LastGC:        lastGC,
		GCCPUFraction: ms.GCCPUFraction,
		Goroutines:    runtime.NumGoroutine(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.MarshalEncode(jsontext.NewEncoder(w, jsontext.WithIndent("  ")), &report)
}
