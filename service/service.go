// Package service provides the DDNS service implementation.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/asusrouter"
	"github.com/database64128/ddns-go/producer/iface"
	"github.com/database64128/ddns-go/producer/ipapi"
	"github.com/database64128/ddns-go/provider"
	"github.com/database64128/ddns-go/provider/cloudflare"
)

// Config contains the configuration options for the DDNS service.
type Config struct {
	// Sources is the configuration for the producer sources.
	Sources []SourceConfig `json:"sources"`

	// Accounts is the configuration for the provider accounts.
	Accounts []AccountConfig `json:"accounts"`

	// Domains is the configuration for the managed domains.
	Domains []DomainConfig `json:"domains"`
}

// Run runs the DDNS service with the provided configuration.
// It blocks until the provided context is canceled.
func (cfg *Config) Run(ctx context.Context, logger *slog.Logger) error {
	producerByName := make(map[string]producer.Producer, len(cfg.Sources))
	for _, sourceCfg := range cfg.Sources {
		if _, ok := producerByName[sourceCfg.Name]; ok {
			return fmt.Errorf("duplicate source: %q", sourceCfg.Name)
		}

		producer, err := sourceCfg.NewProducer(http.DefaultClient)
		if err != nil {
			return fmt.Errorf("failed to create producer %q: %w", sourceCfg.Name, err)
		}
		producerByName[sourceCfg.Name] = producer
	}

	cfAPIClientByName := make(map[string]*cloudflare.Client, len(cfg.Accounts))
	for _, accountCfg := range cfg.Accounts {
		switch accountCfg.Type {
		case "cloudflare":
			if _, ok := cfAPIClientByName[accountCfg.Name]; ok {
				return fmt.Errorf("duplicate account: %q", accountCfg.Name)
			}
			client := cloudflare.NewClient(http.DefaultClient, accountCfg.BearerToken)
			cfAPIClientByName[accountCfg.Name] = client
		default:
			return fmt.Errorf("account %q has unknown type: %q", accountCfg.Name, accountCfg.Type)
		}
	}

	var wg sync.WaitGroup

	domainSet := make(map[string]struct{}, len(cfg.Domains))
	for _, domainCfg := range cfg.Domains {
		if _, ok := domainSet[domainCfg.Domain]; ok {
			return fmt.Errorf("duplicate domain: %q", domainCfg.Domain)
		}
		domainSet[domainCfg.Domain] = struct{}{}

		var v4ch, v6ch <-chan producer.Message
		if domainCfg.IPv4Source != "" {
			producer, ok := producerByName[domainCfg.IPv4Source]
			if !ok {
				return fmt.Errorf("domain %q has unknown IPv4 source: %q", domainCfg.Domain, domainCfg.IPv4Source)
			}
			v4ch = producer.Subscribe()
		}
		if domainCfg.IPv6Source != "" {
			producer, ok := producerByName[domainCfg.IPv6Source]
			if !ok {
				return fmt.Errorf("domain %q has unknown IPv6 source: %q", domainCfg.Domain, domainCfg.IPv6Source)
			}
			v6ch = producer.Subscribe()
		}

		var keeper provider.RecordKeeper
		switch domainCfg.Provider {
		case "cloudflare":
			client, ok := cfAPIClientByName[domainCfg.Account]
			if !ok {
				return fmt.Errorf("domain %q has unknown account: %q", domainCfg.Domain, domainCfg.Account)
			}
			keeper = cloudflare.NewKeeper(domainCfg.Domain, client, domainCfg.Cloudflare)
		default:
			return fmt.Errorf("domain %q has unknown provider: %q", domainCfg.Domain, domainCfg.Provider)
		}

		dm := NewDomainManager(v4ch, v6ch, keeper)
		dmLogger := logger.With("domain", domainCfg.Domain)
		wg.Add(1)
		go func() {
			defer wg.Done()
			dm.Run(ctx, dmLogger)
		}()
	}

	wg.Add(len(producerByName))
	for producerName, producer := range producerByName {
		producerLogger := logger.With("producer", producerName)
		go func() {
			defer wg.Done()
			if err := producer.Run(ctx, producerLogger); err != nil {
				producerLogger.LogAttrs(ctx, slog.LevelError, "Producer failed", slog.Any("error", err))
			}
		}()
	}

	wg.Wait()
	return nil
}

// SourceConfig contains configuration options for a producer source.
type SourceConfig struct {
	// Name is the name of the source.
	Name string `json:"name"`

	// Type is the type of the source.
	//
	//   - "asusrouter": ASUS router.
	//   - "ipapi": IP address API.
	//   - "iface": Network interface.
	Type string `json:"type"`

	// ASUSRouter is the producer configuration for an ASUS router source.
	ASUSRouter asusrouter.ProducerConfig `json:"asusrouter"`

	// IPAPI is the producer configuration for an IP address API source.
	IPAPI ipapi.ProducerConfig `json:"ipapi"`

	// Iface is the producer configuration for a network interface source.
	Iface iface.ProducerConfig `json:"iface"`
}

// NewProducer creates a new [producer.Producer] from the configuration.
func (cfg *SourceConfig) NewProducer(client *http.Client) (producer.Producer, error) {
	switch cfg.Type {
	case "asusrouter":
		return cfg.ASUSRouter.NewProducer(client)
	case "ipapi":
		return cfg.IPAPI.NewProducer(client)
	case "iface":
		return cfg.Iface.NewProducer()
	default:
		return nil, fmt.Errorf("unknown source type: %q", cfg.Type)
	}
}

// AccountConfig contains configuration options for a provider account.
type AccountConfig struct {
	// Name is the name of the account.
	Name string `json:"name"`

	// Type is the type of the account.
	//
	//   - "cloudflare": Cloudflare.
	Type string `json:"type"`

	// Bearertoken is the bearer token for the account.
	BearerToken string `json:"bearer_token"`
}

// DomainConfig contains configuration options for a managed domain.
type DomainConfig struct {
	// Domain is the domain to manage.
	Domain string `json:"domain"`

	// Provider is the DNS provider for the domain.
	//
	//   - "cloudflare": Cloudflare.
	Provider string `json:"provider"`

	// Cloudflare is the configuration for a Cloudflare domain.
	Cloudflare cloudflare.KeeperConfig `json:"cloudflare"`

	// Account is the name of the provider account to use.
	Account string `json:"account"`

	// IPv4Source is the name of the source for the domain's IPv4 address.
	// If empty, the domain's IPv4 address is not managed.
	IPv4Source string `json:"ipv4_source"`

	// IPv6Source is the name of the source for the domain's IPv6 address.
	// If empty, the domain's IPv6 address is not managed.
	IPv6Source string `json:"ipv6_source"`
}

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
	done := ctx.Done()

	for {
		switch m.state {
		case domainManagerStateFetching:
			if err := m.keeper.FetchRecords(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to fetch records", slog.Any("error", err))
				select {
				case <-done:
					return
				case <-time.After(time.Minute):
				}
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Fetched records")
			m.state = domainManagerStateFeeding

		case domainManagerStateFeeding:
			msg := m.cachedMessage

			select {
			case <-done:
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
				select {
				case <-done:
					return
				case <-time.After(time.Minute):
				}
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Synced records")
			m.state = domainManagerStateFeeding

		default:
			panic("unreachable")
		}
	}
}
