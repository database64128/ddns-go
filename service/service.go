// Package service provides the DDNS service implementation.
package service

import (
	"context"
	"log/slog"

	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/provider"
)

// domainManagerState represents the state of a domain manager.
type domainManagerState uint

const (
	domainManagerStateFetching domainManagerState = iota
	domainManagerStateFeeding
	domainManagerStateSyncing
)

// DomainManager manages the DNS records of a domain.
type DomainManager struct {
	v4ch   <-chan producer.Message
	v6ch   <-chan producer.Message
	keeper provider.RecordKeeper

	state         domainManagerState
	cachedMessage producer.Message
}

// NewDomainManager creates a new [DomainManager].
func NewDomainManager(v4ch, v6ch <-chan producer.Message, keeper provider.RecordKeeper) *DomainManager {
	return &DomainManager{
		v4ch:   v4ch,
		v6ch:   v6ch,
		keeper: keeper,
	}
}

// Run initiates the domain manager's record management process.
// It blocks until the provided context is canceled.
func (m *DomainManager) Run(ctx context.Context, logger *slog.Logger) {
	for {
		switch m.state {
		case domainManagerStateFetching:
			if err := m.keeper.FetchRecords(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to fetch records", slog.Any("error", err))
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Fetched records")
			m.state = domainManagerStateFeeding

		case domainManagerStateFeeding:
			msg := m.cachedMessage

			select {
			case <-ctx.Done():
				return
			case v4msg := <-m.v4ch:
				msg.IPv4 = v4msg.IPv4
			case v6msg := <-m.v6ch:
				msg.IPv6 = v6msg.IPv6
			}

			if msg == m.cachedMessage {
				continue
			}

			if !msg.IPv4.IsValid() && m.v4ch != nil {
				v4msg := <-m.v4ch
				msg.IPv4 = v4msg.IPv4
			}
			if !msg.IPv6.IsValid() && m.v6ch != nil {
				v6msg := <-m.v6ch
				msg.IPv6 = v6msg.IPv6
			}

			m.keeper.FeedSourceState(msg)
			logger.LogAttrs(ctx, slog.LevelInfo, "Fed source state", slog.Any("v4", msg.IPv4), slog.Any("v6", msg.IPv6))
			m.state = domainManagerStateSyncing
			m.cachedMessage = msg

		case domainManagerStateSyncing:
			if err := m.keeper.SyncRecords(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to sync records", slog.Any("error", err))
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Synced records")
			m.state = domainManagerStateFeeding

		default:
			panic("unreachable")
		}
	}
}
