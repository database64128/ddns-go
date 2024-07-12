package poller

import (
	"context"
	"log/slog"
	"time"

	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
)

// Poller polls the source periodically and broadcasts the received IP address message to subscribers.
//
// Poller implements [producer.Source] and [producer.Producer].
type Poller struct {
	interval    time.Duration
	source      producer.Source
	broadcaster *broadcaster.Broadcaster
}

// New creates a new [Poller].
func New(interval time.Duration, source producer.Source) *Poller {
	return &Poller{
		interval:    interval,
		source:      source,
		broadcaster: broadcaster.New(),
	}
}

var _ producer.Producer = (*Poller)(nil)

// Snapshot exposes the inner source's Snapshot method.
//
// Snapshot implements [producer.Source.Snapshot].
func (p *Poller) Snapshot(ctx context.Context) (producer.Message, error) {
	return p.source.Snapshot(ctx)
}

// Subscribe returns a channel for receiving updates on IP address changes.
//
// Subscribe implements [producer.Producer.Subscribe].
func (p *Poller) Subscribe() <-chan producer.Message {
	return p.broadcaster.Subscribe()
}

// Run starts the polling process. It logs errors and stops when the context is canceled.
//
// Run implements [producer.Producer.Run].
func (p *Poller) Run(ctx context.Context, logger *slog.Logger) error {
	done := ctx.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.poll(ctx, logger)

	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			p.poll(ctx, logger)
		}
	}
}

func (p *Poller) poll(ctx context.Context, logger *slog.Logger) {
	msg, err := p.source.Snapshot(ctx)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelWarn, "Failed to poll source", slog.Any("error", err))
		return
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "Polled source", slog.Any("v4", msg.IPv4), slog.Any("v6", msg.IPv6))
	p.broadcaster.Broadcast(msg)
}
