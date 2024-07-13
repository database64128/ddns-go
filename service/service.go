// Package service provides the DDNS service implementation.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/database64128/ddns-go/internal/jsonhelper"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/asusrouter"
	"github.com/database64128/ddns-go/producer/bsdroute"
	"github.com/database64128/ddns-go/producer/iface"
	"github.com/database64128/ddns-go/producer/ipapi"
	"github.com/database64128/ddns-go/producer/win32iphlp"
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

	// StartupDelay is the amount of time to wait before starting the service.
	// This can be useful if the service is started before the network is ready.
	StartupDelay jsonhelper.Duration `json:"startup_delay"`
}

// NewService creates a new [Service] from the configuration.
func (cfg *Config) NewService() (*Service, error) {
	if len(cfg.Sources) == 0 {
		return nil, errors.New("no sources configured")
	}

	producerByName := make(map[string]producer.Producer, len(cfg.Sources))
	for _, sourceCfg := range cfg.Sources {
		if _, ok := producerByName[sourceCfg.Name]; ok {
			return nil, fmt.Errorf("duplicate source: %q", sourceCfg.Name)
		}

		producer, err := sourceCfg.NewProducer(http.DefaultClient)
		if err != nil {
			return nil, fmt.Errorf("failed to create producer %q: %w", sourceCfg.Name, err)
		}
		producerByName[sourceCfg.Name] = producer
	}

	cfAPIClientByName := make(map[string]*cloudflare.Client, len(cfg.Accounts))
	for _, accountCfg := range cfg.Accounts {
		switch accountCfg.Type {
		case "cloudflare":
			if _, ok := cfAPIClientByName[accountCfg.Name]; ok {
				return nil, fmt.Errorf("duplicate account: %q", accountCfg.Name)
			}
			if accountCfg.BearerToken == "" {
				return nil, fmt.Errorf("bearer token not specified for account %q", accountCfg.Name)
			}
			client := cloudflare.NewClient(http.DefaultClient, accountCfg.BearerToken)
			cfAPIClientByName[accountCfg.Name] = client
		default:
			return nil, fmt.Errorf("account %q has unknown type: %q", accountCfg.Name, accountCfg.Type)
		}
	}

	domainManagerByDomain := make(map[string]*DomainManager, len(cfg.Domains))
	for i, domainCfg := range cfg.Domains {
		if domainCfg.Domain == "" {
			return nil, fmt.Errorf("unspecified domain at domains[%d]", i)
		}
		if _, ok := domainManagerByDomain[domainCfg.Domain]; ok {
			return nil, fmt.Errorf("duplicate domain: %q", domainCfg.Domain)
		}

		var v4ch, v6ch <-chan producer.Message
		if domainCfg.IPv4Source != "" {
			producer, ok := producerByName[domainCfg.IPv4Source]
			if !ok {
				return nil, fmt.Errorf("domain %q has unknown IPv4 source: %q", domainCfg.Domain, domainCfg.IPv4Source)
			}
			v4ch = producer.Subscribe()
		}
		if domainCfg.IPv6Source != "" {
			producer, ok := producerByName[domainCfg.IPv6Source]
			if !ok {
				return nil, fmt.Errorf("domain %q has unknown IPv6 source: %q", domainCfg.Domain, domainCfg.IPv6Source)
			}
			v6ch = producer.Subscribe()
		}

		var (
			keeper provider.RecordKeeper
			err    error
		)
		switch domainCfg.Provider {
		case "cloudflare":
			client, ok := cfAPIClientByName[domainCfg.Account]
			if !ok {
				return nil, fmt.Errorf("domain %q has unknown account: %q", domainCfg.Domain, domainCfg.Account)
			}
			keeper, err = cloudflare.NewKeeper(domainCfg.Domain, client, domainCfg.Cloudflare)
		default:
			return nil, fmt.Errorf("domain %q has unknown provider: %q", domainCfg.Domain, domainCfg.Provider)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create record keeper for domain %q: %w", domainCfg.Domain, err)
		}

		domainManagerByDomain[domainCfg.Domain] = NewDomainManager(v4ch, v6ch, keeper)
	}

	startupDelay := max(0, cfg.StartupDelay.Value())

	return &Service{
		startupDelay:          startupDelay,
		domainManagerByDomain: domainManagerByDomain,
		producerByName:        producerByName,
	}, nil
}

// Service is the DDNS service.
type Service struct {
	startupDelay          time.Duration
	domainManagerByDomain map[string]*DomainManager
	producerByName        map[string]producer.Producer
}

// Run starts the DDNS service.
// It blocks until the provided context is canceled.
func (s *Service) Run(ctx context.Context, logger *slog.Logger) {
	if s.startupDelay > 0 {
		logger.LogAttrs(ctx, slog.LevelInfo, "Waiting before starting service", slog.Duration("delay", s.startupDelay))
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.startupDelay):
		}
	}

	var wg sync.WaitGroup
	wg.Add(len(s.domainManagerByDomain) + len(s.producerByName))

	for domain, dm := range s.domainManagerByDomain {
		dmLogger := logger.With("domain", domain)
		dmLogger.LogAttrs(ctx, slog.LevelInfo, "Starting domain manager")
		go func() {
			defer wg.Done()
			dm.Run(ctx, dmLogger)
			dmLogger.LogAttrs(ctx, slog.LevelInfo, "Stopped domain manager")
		}()
	}

	for producerName, producer := range s.producerByName {
		producerLogger := logger.With("producer", producerName)
		producerLogger.LogAttrs(ctx, slog.LevelInfo, "Starting producer")
		go func() {
			defer wg.Done()
			if err := producer.Run(ctx, producerLogger); err != nil {
				producerLogger.LogAttrs(ctx, slog.LevelError, "Producer failed", slog.Any("error", err))
				return
			}
			producerLogger.LogAttrs(ctx, slog.LevelInfo, "Stopped producer")
		}()
	}

	logger.LogAttrs(ctx, slog.LevelInfo, "Service started")
	wg.Wait()
	logger.LogAttrs(ctx, slog.LevelInfo, "Service stopped")
}

// SourceConfig contains configuration options for a producer source.
type SourceConfig struct {
	// Name is the name of the source.
	Name string `json:"name"`

	// Type is the type of the source.
	//
	//   - "asusrouter": ASUS router.
	//   - "ipapi": IP address API.
	//   - "iface": Network interface (generic).
	//   - "bsdroute": Network interface (Darwin, DragonFly BSD, FreeBSD, NetBSD, OpenBSD).
	//   - "win32iphlp": Network interface (Windows).
	Type string `json:"type"`

	// ASUSRouter is the producer configuration for an ASUS router source.
	ASUSRouter asusrouter.ProducerConfig `json:"asusrouter"`

	// IPAPI is the producer configuration for an IP address API source.
	IPAPI ipapi.ProducerConfig `json:"ipapi"`

	// Iface is the producer configuration for a generic network interface source.
	Iface iface.ProducerConfig `json:"iface"`

	// BSDRoute is the producer configuration for a bsdroute network interface source.
	BSDRoute bsdroute.ProducerConfig `json:"bsdroute"`

	// Win32IPHLP is the producer configuration for a win32iphlp network interface source.
	Win32IPHLP win32iphlp.ProducerConfig `json:"win32iphlp"`
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
	case "bsdroute":
		return cfg.BSDRoute.NewProducer()
	case "win32iphlp":
		return cfg.Win32IPHLP.NewProducer()
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
	domainManagerStateInitialWait domainManagerState = iota
	domainManagerStateUpdateWait
	domainManagerStateFetching
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
		case domainManagerStateInitialWait:
			if m.v4ch != nil {
				select {
				case <-done:
					return
				case v4msg := <-m.v4ch:
					m.cachedMessage.IPv4 = v4msg.IPv4
				}
			}

			if m.v6ch != nil {
				select {
				case <-done:
					return
				case v6msg := <-m.v6ch:
					m.cachedMessage.IPv6 = v6msg.IPv6
				}
			}

			m.keeper.FeedSourceState(m.cachedMessage)
			logger.LogAttrs(ctx, slog.LevelInfo, "Fed source state", slog.Any("v4", m.cachedMessage.IPv4), slog.Any("v6", m.cachedMessage.IPv6))
			m.state = domainManagerStateFetching

		case domainManagerStateUpdateWait:
			msg := m.cachedMessage

			select {
			case <-done:
				return
			case v4msg := <-m.v4ch:
				msg.IPv4 = v4msg.IPv4
				select {
				case v6msg := <-m.v6ch:
					msg.IPv6 = v6msg.IPv6
				default:
				}
			case v6msg := <-m.v6ch:
				msg.IPv6 = v6msg.IPv6
				select {
				case v4msg := <-m.v4ch:
					msg.IPv4 = v4msg.IPv4
				default:
				}
			}

			if msg == m.cachedMessage {
				continue
			}

			m.keeper.FeedSourceState(msg)
			logger.LogAttrs(ctx, slog.LevelInfo, "Fed source state", slog.Any("v4", msg.IPv4), slog.Any("v6", msg.IPv6))
			m.state = domainManagerStateSyncing
			m.cachedMessage = msg

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
			m.state = domainManagerStateSyncing

		case domainManagerStateSyncing:
			if err := m.keeper.SyncRecords(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to sync records", slog.Any("error", err))
				switch err {
				case provider.ErrKeeperFeedFirst:
					m.state = domainManagerStateUpdateWait
				case provider.ErrKeeperFetchFirst:
					m.state = domainManagerStateFetching
				default:
					if errors.Is(err, provider.ErrAPIResponseFailure) {
						// The failure could be caused by outdated cached records.
						// Fetch the records again to ensure they are up-to-date.
						m.state = domainManagerStateFetching
					}
					select {
					case <-done:
						return
					case <-time.After(time.Minute):
					}
				}
				continue
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "Synced records")
			m.state = domainManagerStateUpdateWait

		default:
			panic("unreachable")
		}
	}
}
