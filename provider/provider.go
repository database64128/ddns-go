package provider

import (
	"context"
	"errors"

	"github.com/database64128/ddns-go/producer"
)

var (
	ErrKeeperFetchFirst = errors.New("call FetchRecords first")
	ErrKeeperFeedFirst  = errors.New("call FeedSourceState first")
)

// RecordKeeper interacts with a DNS provider to manage a domain's DNS records.
type RecordKeeper interface {
	// FetchRecords fetches the state of the domain's managed DNS records.
	FetchRecords(ctx context.Context) error

	// FeedSourceState feeds the current IP addresses of the source to the record keeper.
	FeedSourceState(msg producer.Message)

	// SyncRecords synchronizes the domain's managed DNS records with the source state,
	// creating or updating records as needed.
	SyncRecords(ctx context.Context) error
}

// BaseKeeperConfig contains universal configuration options for a record keeper.
type BaseKeeperConfig struct {
	// Domain is the domain to manage.
	Domain string `json:"domain"`

	// Provider is the DNS provider for the domain.
	//
	//   - "cloudflare": Cloudflare.
	Provider string `json:"provider"`

	// Account is the name of the provider account to use.
	Account string `json:"account"`

	// IPv4Source is the name of the source for the domain's IPv4 address.
	// If empty, the domain's IPv4 address is not managed.
	IPv4Source string `json:"ipv4_source"`

	// IPv6Source is the name of the source for the domain's IPv6 address.
	// If empty, the domain's IPv6 address is not managed.
	IPv6Source string `json:"ipv6_source"`
}
