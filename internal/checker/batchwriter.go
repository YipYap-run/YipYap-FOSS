package checker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const flushInterval = 500 * time.Millisecond

// batchWriter accumulates MonitorCheck results in a buffer and flushes them
// to the store. Multiple concurrent drain loops prevent a slow DB flush from
// blocking the channel and causing drops.
type batchWriter struct {
	store            store.CheckStore
	ch               chan *domain.MonitorCheck
	wg               sync.WaitGroup
	batchSize        int
	flushConcurrency int
}

func newBatchWriter(cs store.CheckStore, cfg CheckerConfig) *batchWriter {
	bs := cfg.batchSize()
	bw := &batchWriter{
		store:            cs,
		ch:               make(chan *domain.MonitorCheck, cfg.channelSize()),
		batchSize:        bs,
		flushConcurrency: cfg.flushConcurrency(),
	}
	for range cfg.batchWriters() {
		bw.wg.Add(1)
		go bw.loop()
	}
	return bw
}

// Enqueue adds a check result to the write buffer. Non-blocking; if the
// buffer is full the check is dropped with a warning (prefer losing a single
// data point over blocking a worker).
func (bw *batchWriter) Enqueue(c *domain.MonitorCheck) {
	select {
	case bw.ch <- c:
	default:
		slog.Warn("batch writer buffer full, dropping check", "monitor_id", c.MonitorID)
	}
}

// Stop drains remaining checks and returns once all are flushed.
func (bw *batchWriter) Stop() {
	close(bw.ch)
	bw.wg.Wait()
}

func (bw *batchWriter) loop() {
	defer bw.wg.Done()

	buf := make([]*domain.MonitorCheck, 0, bw.batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case c, ok := <-bw.ch:
			if !ok {
				if len(buf) > 0 {
					bw.flush(buf)
				}
				return
			}
			buf = append(buf, c)
			if len(buf) >= bw.batchSize {
				bw.flush(buf)
				buf = make([]*domain.MonitorCheck, 0, bw.batchSize)
			}
		case <-ticker.C:
			if len(buf) > 0 {
				bw.flush(buf)
				buf = make([]*domain.MonitorCheck, 0, bw.batchSize)
			}
		}
	}
}

func (bw *batchWriter) flush(batch []*domain.MonitorCheck) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch := make(chan *domain.MonitorCheck, len(batch))
	for _, c := range batch {
		ch <- c
	}
	close(ch)

	workers := min(bw.flushConcurrency, len(batch))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range ch {
				if err := bw.store.Create(ctx, c); err != nil {
					slog.Error("batch writer: failed to store check", "monitor_id", c.MonitorID, "error", err)
				}
			}
		}()
	}
	wg.Wait()
}
