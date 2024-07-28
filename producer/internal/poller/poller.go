package poller

import (
	"context"
	"log/slog"
	"time"

	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/tslog"
)

// Poller polls the source periodically and broadcasts the received IP address message to subscribers.
//
// Poller implements [producer.Source] and [producer.Producer].
type Poller struct {
	interval    time.Duration
	source      producer.Source
	logger      *tslog.Logger
	broadcaster *broadcaster.Broadcaster

	cachedMessage producer.Message
}

// New creates a new [Poller].
func New(interval time.Duration, source producer.Source, logger *tslog.Logger) *Poller {
	return &Poller{
		interval:    interval,
		source:      source,
		logger:      logger,
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
func (p *Poller) Run(ctx context.Context) {
	p.logger.Info("Started polling source", slog.Duration("interval", p.interval))
	defer p.logger.Info("Stopped polling source")

	done := ctx.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	p.poll(ctx, false)

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			p.poll(ctx, true)
		}
	}
}

func (p *Poller) poll(ctx context.Context, skipUnchanged bool) {
	msg, err := p.source.Snapshot(ctx)
	if err != nil {
		p.logger.Warn("Failed to poll source", tslog.Err(err))
		return
	}

	if p.logger.Enabled(slog.LevelInfo) {
		p.logger.Info("Polled source",
			tslog.Addr("v4", msg.IPv4),
			tslog.Addr("v6", msg.IPv6),
		)
	}

	if skipUnchanged && msg == p.cachedMessage {
		return
	}
	p.cachedMessage = msg

	p.broadcaster.Broadcast(msg)
}
