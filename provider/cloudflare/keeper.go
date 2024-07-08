package cloudflare

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/provider"
)

// KeeperConfig contains configuration options specific to the Cloudflare domain DNS record keeper.
type KeeperConfig struct {
	// ZoneID is the ID of the Cloudflare zone.
	ZoneID string `json:"zone_id"`

	// ARecord is the configuration for the managed A record.
	ARecord RecordConfig `json:"a_record"`

	// AAAARecord is the configuration for the managed AAAA record.
	AAAARecord RecordConfig `json:"aaaa_record"`

	// HTTPSRecord is the configuration for the managed HTTPS record.
	HTTPSRecord RecordConfig `json:"https_record"`
}

// RecordConfig contains configuration options for a managed DNS record.
type RecordConfig struct {
	// Enabled controls whether to manage the record.
	Enabled bool `json:"enabled"`

	// Priority is the SvcPriority of the managed HTTPS DNS record.
	//
	// Per RFC 9460:
	//
	//	When SvcPriority is 0, the SVCB record is in AliasMode (Section 2.4.2).
	//	Otherwise, it is in ServiceMode (Section 2.4.3).
	Priority uint16 `json:"priority"`

	// Proxied controls whether to proxy the A/AAAA record through Cloudflare.
	Proxied bool `json:"proxied"`

	// TTL is the TTL of the managed DNS record in seconds.
	// Setting to 1 means 'automatic'. Value must be between 60 and 86400,
	// with the minimum reduced to 30 for Enterprise zones.
	TTL int `json:"ttl"`

	// Target is the target of the managed HTTPS DNS record.
	// If empty, "." is used.
	Target string `json:"target"`

	// SvcParams are additional SvcParamKey=SvcParamValue pairs for the managed HTTPS DNS record.
	// Depending on the configured sources, "ipv4hint" and/or "ipv6hint" may not be allowed in the list.
	SvcParams string `json:"svc_params"`

	// Comment is a comment about the managed DNS record.
	Comment string `json:"comment"`

	// Tags are custom tags for the managed DNS record.
	Tags []string `json:"tags"`
}

// Keeper interacts with Cloudflare's API to manage a domain's DNS records.
//
// Keeper implements [provider.RecordKeeper].
type Keeper struct {
	domain string
	client *Client
	config KeeperConfig

	sourceIPv4Text []byte
	sourceIPv6Text []byte
	aRecord        DNSRecord
	aaaaRecord     DNSRecord
	httpsRecord    DNSRecord
	hasFetched     bool
}

// NewKeeper creates a new [Keeper].
func NewKeeper(domain string, client *Client, config KeeperConfig) *Keeper {
	return &Keeper{
		domain: domain,
		client: client,
		config: config,
	}
}

var _ provider.RecordKeeper = (*Keeper)(nil)

// clearFetched clears the fetched state of the managed records.
func (k *Keeper) clearFetched() {
	k.aRecord.ID = ""
	k.aaaaRecord.ID = ""
	k.httpsRecord.ID = ""
	k.hasFetched = false
}

// FetchRecords fetches the state of the domain's managed DNS records.
//
// FetchRecords implements [provider.RecordKeeper.FetchRecords].
func (k *Keeper) FetchRecords(ctx context.Context) error {
	k.clearFetched()

	records, err := k.client.ListDNSRecords(ctx, k.config.ZoneID, &ListDNSRecordsRequest{
		Name: k.domain,
	})
	if err != nil {
		return fmt.Errorf("failed to list DNS records: %w", err)
	}

	for _, record := range records {
		switch record.Type {
		case "A":
			if k.config.ARecord.Enabled {
				if k.aRecord.ID != "" {
					return fmt.Errorf("unsupported additional A record with ID %q", record.ID)
				}
				k.aRecord = record
			}
		case "AAAA":
			if k.config.AAAARecord.Enabled {
				if k.aaaaRecord.ID != "" {
					return fmt.Errorf("unsupported additional AAAA record with ID %q", record.ID)
				}
				k.aaaaRecord = record
			}
		case "HTTPS":
			if k.config.HTTPSRecord.Enabled {
				if k.httpsRecord.ID != "" {
					return fmt.Errorf("duplicate HTTPS record with ID %q", record.ID)
				}
				k.httpsRecord = record
			}
		case "CNAME":
			return fmt.Errorf("found CNAME record with ID %q", record.ID)
		}
	}

	k.hasFetched = true
	return nil
}

// FeedSourceState feeds the current IP addresses of the source to the record keeper.
//
// FeedSourceState implements [provider.RecordKeeper.FeedSourceState].
func (k *Keeper) FeedSourceState(msg producer.Message) {
	k.sourceIPv4Text = msg.IPv4.AppendTo(k.sourceIPv4Text[:0])
	k.sourceIPv6Text = msg.IPv6.AppendTo(k.sourceIPv6Text[:0])
}

// isFed returns whether the source state satisfies the requirements of the managed records.
func (k *Keeper) isFed() bool {
	if k.config.ARecord.Enabled && len(k.sourceIPv4Text) == 0 {
		return false
	}
	if k.config.AAAARecord.Enabled && len(k.sourceIPv6Text) == 0 {
		return false
	}
	if k.config.HTTPSRecord.Enabled && len(k.sourceIPv4Text) == 0 && len(k.sourceIPv6Text) == 0 {
		return false
	}
	return true
}

// SyncRecords synchronizes the domain's managed DNS records with the source state,
// creating or updating records as needed.
//
// SyncRecords implements [provider.RecordKeeper.SyncRecords].
func (k *Keeper) SyncRecords(ctx context.Context) error {
	if !k.hasFetched {
		return provider.ErrKeeperFetchFirst
	}
	if !k.isFed() {
		return provider.ErrKeeperFeedFirst
	}

	if k.config.ARecord.Enabled {
		if err := k.syncARecord(ctx); err != nil {
			return fmt.Errorf("failed to sync A record: %w", err)
		}
	}
	if k.config.AAAARecord.Enabled {
		if err := k.syncAAAARecord(ctx); err != nil {
			return fmt.Errorf("failed to sync AAAA record: %w", err)
		}
	}
	if k.config.HTTPSRecord.Enabled {
		if err := k.syncHTTPSRecord(ctx); err != nil {
			return fmt.Errorf("failed to sync HTTPS record: %w", err)
		}
	}

	return nil
}

// syncARecord synchronizes the managed A record with the source state.
func (k *Keeper) syncARecord(ctx context.Context) (err error) {
	if k.aRecord.ID == "" {
		k.aRecord, err = k.client.CreateDNSRecord(ctx, k.config.ZoneID, &DNSRecord{
			Name:    k.domain,
			Type:    "A",
			Content: string(k.sourceIPv4Text),
			Proxied: k.config.ARecord.Proxied,
			TTL:     k.config.ARecord.TTL,
			Comment: k.config.ARecord.Comment,
			Tags:    k.config.ARecord.Tags,
		})
		if err != nil {
			return fmt.Errorf("failed to create A record: %w", err)
		}
		return nil
	}

	// Future consideration: We don't know if the API guarantees the order of tags.
	// Maybe we should sort the tags before comparing them.
	if k.aRecord.Content == string(k.sourceIPv4Text) &&
		k.aRecord.Proxied == k.config.ARecord.Proxied &&
		k.aRecord.TTL == k.config.ARecord.TTL &&
		k.aRecord.Comment == k.config.ARecord.Comment &&
		slices.Equal(k.aRecord.Tags, k.config.ARecord.Tags) {
		return nil
	}

	record, err := k.client.UpdateDNSRecord(ctx, k.config.ZoneID, k.aRecord.ID, &UpdateDNSRecordRequest{
		Content: string(k.sourceIPv4Text),
		Proxied: &k.config.ARecord.Proxied,
		TTL:     k.config.ARecord.TTL,
		Comment: &k.config.ARecord.Comment,
		Tags:    k.config.ARecord.Tags,
	})
	if err != nil {
		return fmt.Errorf("failed to update A record: %w", err)
	}
	k.aRecord = record

	return nil
}

// syncAAAARecord synchronizes the managed AAAA record with the source state.
func (k *Keeper) syncAAAARecord(ctx context.Context) (err error) {
	if k.aaaaRecord.ID == "" {
		k.aaaaRecord, err = k.client.CreateDNSRecord(ctx, k.config.ZoneID, &DNSRecord{
			Name:    k.domain,
			Type:    "AAAA",
			Content: string(k.sourceIPv6Text),
			Proxied: k.config.AAAARecord.Proxied,
			TTL:     k.config.AAAARecord.TTL,
			Comment: k.config.AAAARecord.Comment,
			Tags:    k.config.AAAARecord.Tags,
		})
		if err != nil {
			return fmt.Errorf("failed to create AAAA record: %w", err)
		}
		return nil
	}

	// Future consideration: We don't know if the API guarantees the order of tags.
	// Maybe we should sort the tags before comparing them.
	if k.aaaaRecord.Content == string(k.sourceIPv6Text) &&
		k.aaaaRecord.Proxied == k.config.AAAARecord.Proxied &&
		k.aaaaRecord.TTL == k.config.AAAARecord.TTL &&
		k.aaaaRecord.Comment == k.config.AAAARecord.Comment &&
		slices.Equal(k.aaaaRecord.Tags, k.config.AAAARecord.Tags) {
		return nil
	}

	record, err := k.client.UpdateDNSRecord(ctx, k.config.ZoneID, k.aaaaRecord.ID, &UpdateDNSRecordRequest{
		Content: string(k.sourceIPv6Text),
		Proxied: &k.config.AAAARecord.Proxied,
		TTL:     k.config.AAAARecord.TTL,
		Comment: &k.config.AAAARecord.Comment,
		Tags:    k.config.AAAARecord.Tags,
	})
	if err != nil {
		return fmt.Errorf("failed to update AAAA record: %w", err)
	}
	k.aaaaRecord = record

	return nil
}

// buildSvcParams builds the SvcParams for the managed HTTPS DNS record.
func (k *Keeper) buildSvcParams() string {
	var sb strings.Builder
	sb.Grow(len(`ipv4hint="" ipv6hint="" `) + len(k.sourceIPv4Text) + len(k.sourceIPv6Text) + len(k.config.HTTPSRecord.SvcParams))
	if len(k.sourceIPv4Text) > 0 {
		sb.WriteString(`ipv4hint="`)
		sb.Write(k.sourceIPv4Text)
		sb.WriteString(`" `)
	}
	if len(k.sourceIPv6Text) > 0 {
		sb.WriteString(`ipv6hint="`)
		sb.Write(k.sourceIPv6Text)
		sb.WriteString(`" `)
	}
	sb.WriteString(k.config.HTTPSRecord.SvcParams)
	return sb.String()
}

// syncHTTPSRecord synchronizes the managed HTTPS record with the source state.
func (k *Keeper) syncHTTPSRecord(ctx context.Context) (err error) {
	svcParams := k.buildSvcParams()

	if k.httpsRecord.ID == "" {
		k.httpsRecord, err = k.client.CreateDNSRecord(ctx, k.config.ZoneID, &DNSRecord{
			Name: k.domain,
			Type: "HTTPS",
			TTL:  k.config.HTTPSRecord.TTL,
			Data: &DNSRecordData{
				Target:   k.config.HTTPSRecord.Target,
				Value:    svcParams,
				Priority: k.config.HTTPSRecord.Priority,
			},
			Comment: k.config.HTTPSRecord.Comment,
			Tags:    k.config.HTTPSRecord.Tags,
		})
		if err != nil {
			return fmt.Errorf("failed to create HTTPS record: %w", err)
		}
		return nil
	}

	// Future consideration: We don't know if the API guarantees the order of tags.
	// Maybe we should sort the tags before comparing them.
	if k.httpsRecord.TTL == k.config.HTTPSRecord.TTL &&
		k.httpsRecord.Data.Target == k.config.HTTPSRecord.Target &&
		k.httpsRecord.Data.Value == svcParams &&
		k.httpsRecord.Data.Priority == k.config.HTTPSRecord.Priority &&
		k.httpsRecord.Comment == k.config.HTTPSRecord.Comment &&
		slices.Equal(k.httpsRecord.Tags, k.config.HTTPSRecord.Tags) {
		return nil
	}

	record, err := k.client.UpdateDNSRecord(ctx, k.config.ZoneID, k.httpsRecord.ID, &UpdateDNSRecordRequest{
		TTL: k.config.HTTPSRecord.TTL,
		Data: &DNSRecordData{
			Target:   k.config.HTTPSRecord.Target,
			Value:    svcParams,
			Priority: k.config.HTTPSRecord.Priority,
		},
		Comment: &k.config.HTTPSRecord.Comment,
		Tags:    k.config.HTTPSRecord.Tags,
	})
	if err != nil {
		return fmt.Errorf("failed to update HTTPS record: %w", err)
	}
	k.httpsRecord = record

	return nil
}
