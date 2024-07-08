package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/database64128/ddns-go/internal/httphelper"
	"github.com/database64128/ddns-go/provider"
)

const (
	baseURL = "https://api.cloudflare.com/client/v4"
)

// Client is a Cloudflare API client for managing DNS records.
type Client struct {
	client              *http.Client
	authorizationHeader string
}

// NewClient creates a new [Client] with the given HTTP client and API token.
func NewClient(client *http.Client, token string) *Client {
	return &Client{
		client:              client,
		authorizationHeader: "Bearer " + token,
	}
}

// ListDNSRecords lists DNS records in a zone.
func (c *Client) ListDNSRecords(ctx context.Context, zoneID string, req *ListDNSRecordsRequest) ([]DNSRecord, error) {
	return clientDo[[]DNSRecord](c.client, c.authorizationHeader, func() (*http.Request, error) {
		return req.NewRequest(ctx, zoneID)
	})
}

// CreateDNSRecord creates a DNS record in a zone.
func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, record *DNSRecord) (DNSRecord, error) {
	return clientDo[DNSRecord](c.client, c.authorizationHeader, func() (*http.Request, error) {
		return record.NewCreateRequest(ctx, zoneID)
	})
}

// UpdateDNSRecord updates a DNS record in a zone.
func (c *Client) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, req *UpdateDNSRecordRequest) (DNSRecord, error) {
	return clientDo[DNSRecord](c.client, c.authorizationHeader, func() (*http.Request, error) {
		return req.NewRequest(ctx, zoneID, recordID)
	})
}

// DeleteDNSRecord deletes a DNS record in a zone.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := clientDo[DNSRecord](c.client, c.authorizationHeader, func() (*http.Request, error) {
		return NewDeleteRequest(ctx, zoneID, recordID)
	})
	return err
}

// ListDNSRecordsRequest contains the query parameters for listing DNS records.
type ListDNSRecordsRequest struct {
	Name              string   // name
	Type              string   // type
	Content           string   // content
	Proxied           *bool    // proxied
	Comment           string   // comment
	CommentAbsent     string   // comment.absent
	CommentContains   string   // comment.contains
	CommentEndsWith   string   // comment.endswith
	CommentExact      string   // comment.exact
	CommentPresent    string   // comment.present
	CommentStartsWith string   // comment.startswith
	Tags              []string // tag
	TagAbsent         string   // tag.absent
	TagContains       string   // tag.contains
	TagEndsWith       string   // tag.endswith
	TagExact          string   // tag.exact
	TagPresent        string   // tag.present
	TagStartsWith     string   // tag.startswith
	TagMatch          string   // tag_match
	Order             string   // order
	Direction         string   // direction
	Match             string   // match
	Search            string   // search
	Page              int      // page
	PerPage           int      // per_page
}

// NewRequest creates a new request to list DNS records.
func (r *ListDNSRecordsRequest) NewRequest(ctx context.Context, zoneID string) (*http.Request, error) {
	values := url.Values{}
	urlValuesSetStringIfNotEmpty(values, "name", r.Name)
	urlValuesSetStringIfNotEmpty(values, "type", r.Type)
	urlValuesSetStringIfNotEmpty(values, "content", r.Content)
	urlValuesSetBoolIfNotNil(values, "proxied", r.Proxied)
	urlValuesSetStringIfNotEmpty(values, "comment", r.Comment)
	urlValuesSetStringIfNotEmpty(values, "comment.absent", r.CommentAbsent)
	urlValuesSetStringIfNotEmpty(values, "comment.contains", r.CommentContains)
	urlValuesSetStringIfNotEmpty(values, "comment.endswith", r.CommentEndsWith)
	urlValuesSetStringIfNotEmpty(values, "comment.exact", r.CommentExact)
	urlValuesSetStringIfNotEmpty(values, "comment.present", r.CommentPresent)
	urlValuesSetStringIfNotEmpty(values, "comment.startswith", r.CommentStartsWith)
	urlValuesSetStringSliceIfNotEmpty(values, "tag", r.Tags)
	urlValuesSetStringIfNotEmpty(values, "tag.absent", r.TagAbsent)
	urlValuesSetStringIfNotEmpty(values, "tag.contains", r.TagContains)
	urlValuesSetStringIfNotEmpty(values, "tag.endswith", r.TagEndsWith)
	urlValuesSetStringIfNotEmpty(values, "tag.exact", r.TagExact)
	urlValuesSetStringIfNotEmpty(values, "tag.present", r.TagPresent)
	urlValuesSetStringIfNotEmpty(values, "tag.startswith", r.TagStartsWith)
	urlValuesSetStringIfNotEmpty(values, "tag_match", r.TagMatch)
	urlValuesSetStringIfNotEmpty(values, "order", r.Order)
	urlValuesSetStringIfNotEmpty(values, "direction", r.Direction)
	urlValuesSetStringIfNotEmpty(values, "match", r.Match)
	urlValuesSetStringIfNotEmpty(values, "search", r.Search)
	urlValuesSetIntIfNotZero(values, "page", r.Page)
	urlValuesSetIntIfNotZero(values, "per_page", r.PerPage)

	url := fmt.Sprintf("%s/zones/%s/dns_records?%s", baseURL, zoneID, values.Encode())
	return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
}

func urlValuesSetStringIfNotEmpty(values url.Values, key, value string) {
	if value != "" {
		values.Set(key, value)
	}
}

func urlValuesSetBoolIfNotNil(values url.Values, key string, value *bool) {
	if value != nil {
		values.Set(key, strconv.FormatBool(*value))
	}
}

func urlValuesSetStringSliceIfNotEmpty(values url.Values, key string, value []string) {
	if len(value) > 0 {
		values[key] = value
	}
}

func urlValuesSetIntIfNotZero(values url.Values, key string, value int) {
	if value != 0 {
		values.Set(key, strconv.Itoa(value))
	}
}

// UpdateDNSRecordRequest is the request body for updating a DNS record.
type UpdateDNSRecordRequest struct {
	Name     string         `json:"name,omitempty"`
	Type     string         `json:"type,omitempty"`
	Content  string         `json:"content,omitempty"`
	Priority uint16         `json:"priority,omitempty"`
	Proxied  *bool          `json:"proxied,omitempty"`
	TTL      int            `json:"ttl,omitempty"`
	Data     *DNSRecordData `json:"data,omitempty"`
	Comment  *string        `json:"comment,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
}

// NewRequest creates a new request to update a DNS record.
func (r *UpdateDNSRecordRequest) NewRequest(ctx context.Context, zoneID, recordID string) (*http.Request, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", baseURL, zoneID, recordID)
	return httphelper.NewJSONRequest(ctx, http.MethodPatch, url, r)
}

// DNSRecord represents a DNS record in a zone.
type DNSRecord struct {
	ID         string         `json:"id"`
	ZoneID     string         `json:"zone_id,omitempty"`
	ZoneName   string         `json:"zone_name,omitempty"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Content    string         `json:"content"`
	Priority   uint16         `json:"priority,omitempty"`
	Proxiable  bool           `json:"proxiable,omitempty"`
	Proxied    bool           `json:"proxied"`
	Locked     bool           `json:"locked,omitempty"`
	TTL        int            `json:"ttl,omitempty"`
	Data       *DNSRecordData `json:"data,omitempty"`
	Meta       *DNSRecordMeta `json:"meta,omitempty"`
	Comment    string         `json:"comment,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	CreatedOn  time.Time      `json:"created_on"`
	ModifiedOn time.Time      `json:"modified_on"`
}

// NewCreateRequest creates a new request to create a DNS record.
func (r *DNSRecord) NewCreateRequest(ctx context.Context, zoneID string) (*http.Request, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records", baseURL, zoneID)
	return httphelper.NewJSONRequest(ctx, http.MethodPost, url, r)
}

// NewDeleteRequest creates a new request to delete a DNS record.
func NewDeleteRequest(ctx context.Context, zoneID, recordID string) (*http.Request, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", baseURL, zoneID, recordID)
	return http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
}

// DNSRecordMeta represents the metadata of a DNS record.
type DNSRecordMeta struct {
	AutoAdded           bool `json:"auto_added"`
	ManagedByApps       bool `json:"managed_by_apps"`
	ManagedByArgoTunnel bool `json:"managed_by_argo_tunnel"`
}

// DNSRecordData represents the data of a DNS record.
type DNSRecordData struct {
	Name     string `json:"name,omitempty"`
	Target   string `json:"target,omitempty"`
	Value    string `json:"value,omitempty"`
	Priority uint16 `json:"priority,omitempty"`
	Weight   uint16 `json:"weight,omitempty"`
	Port     uint16 `json:"port,omitempty"`
}

func clientDo[R any](client *http.Client, authorizationHeader string, newRequest func() (*http.Request, error)) (result R, err error) {
	req, err := newRequest()
	if err != nil {
		return result, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["Authorization"] = []string{authorizationHeader}

	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	buf.Grow(max(0, int(resp.ContentLength)))
	if _, err = io.Copy(&buf, resp.Body); err != nil {
		return result, fmt.Errorf("failed to read response: %w", err)
	}
	bodyBytes := buf.Bytes()

	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("%w: unexpected status code %d: %q", provider.ErrAPIResponseFailure, resp.StatusCode, bodyBytes)
	}

	var response Response[R]
	if err = json.Unmarshal(bodyBytes, &response); err != nil {
		return result, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !response.Success {
		return result, fmt.Errorf("%w: %v", provider.ErrAPIResponseFailure, response.Errors)
	}

	return response.Result, nil
}

// Response is a generic response from the Cloudflare API.
type Response[R any] struct {
	Success    bool           `json:"success"`
	Errors     []ResponseInfo `json:"errors"`
	Messages   []ResponseInfo `json:"messages"`
	Result     R              `json:"result"`
	ResultInfo ResultInfo     `json:"result_info"`
}

// ResponseInfo contains a code and message returned by the API as errors or
// informational messages inside the response.
type ResponseInfo struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ResultInfo contains metadata about the [Response].
type ResultInfo struct {
	Page       int               `json:"page"`
	PerPage    int               `json:"per_page"`
	TotalPages int               `json:"total_pages"`
	Count      int               `json:"count"`
	Total      int               `json:"total_count"`
	Cursor     string            `json:"cursor"`
	Cursors    ResultInfoCursors `json:"cursors"`
}

// ResultInfoCursors contains information about cursors.
type ResultInfoCursors struct {
	Before string `json:"before"`
	After  string `json:"after"`
}
