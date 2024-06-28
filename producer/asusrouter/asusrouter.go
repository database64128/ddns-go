package asusrouter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/database64128/ddns-go/internal/jsonhelper"
	"github.com/database64128/ddns-go/producer"
	"github.com/database64128/ddns-go/producer/internal/broadcaster"
	"github.com/database64128/ddns-go/producer/internal/poller"
)

// Source obtains the WAN IPv4 address from an ASUS router.
//
// Source implements [producer.Source].
type Source struct {
	client          *http.Client
	loginURL        string
	loginReferer    string
	loginBody       []byte
	ajaxStatusURL   string
	ajaxStatusIPKey string
}

// NewSource creates a new [Source].
//
//   - If client is nil, [http.DefaultClient] is used.
//   - If baseURL is empty, it defaults to "http://router.asus.com".
//   - If ajaxStatusIPKey is empty, it defaults to "wan0_ipaddr=".
func NewSource(client *http.Client, baseURL, username, password, ajaxStatusIPKey string) (*Source, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "http://router.asus.com"
	}
	if ajaxStatusIPKey == "" {
		ajaxStatusIPKey = "wan0_ipaddr="
	}

	loginURL, err := url.JoinPath(baseURL, "/login.cgi")
	if err != nil {
		return nil, fmt.Errorf("failed to join login URL: %w", err)
	}

	loginReferer, err := url.JoinPath(baseURL, "/Main_Login.asp")
	if err != nil {
		return nil, fmt.Errorf("failed to join login referer: %w", err)
	}

	loginBody := base64.URLEncoding.AppendEncode([]byte("login_authorization="), []byte(username+":"+password))

	ajaxStatusURL, err := url.JoinPath(baseURL, "/ajax_status.xml")
	if err != nil {
		return nil, fmt.Errorf("failed to join AJAX status URL: %w", err)
	}

	return &Source{
		client:          client,
		loginURL:        loginURL,
		loginReferer:    loginReferer,
		loginBody:       loginBody,
		ajaxStatusURL:   ajaxStatusURL,
		ajaxStatusIPKey: ajaxStatusIPKey,
	}, nil
}

var _ producer.Source = (*Source)(nil)

var errLoginNoCookie = errors.New("no asus_token cookie found in login response, likely wrong username or password")

// Snapshot returns the current WAN IPv4 address from the ASUS router.
//
// Snapshot implements [producer.Source.Snapshot].
func (s *Source) Snapshot(ctx context.Context) (producer.Message, error) {
	// Perform a login request to obtain a session cookie.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.loginURL, bytes.NewReader(s.loginBody))
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", s.loginReferer) // The request will fail without a Referer.

	resp, err := s.client.Do(req)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to send login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return producer.Message{}, fmt.Errorf("login failed with status %d", resp.StatusCode)
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "asus_token" {
			cookie = c
			break
		}
	}
	if cookie == nil {
		// The server always returns 200 OK without any useful information.
		// So we simply return an error if the cookie is not found.
		return producer.Message{}, errLoginNoCookie
	}

	// Perform an AJAX status request to obtain the WAN IP address.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, s.ajaxStatusURL, nil)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to create AJAX status request: %w", err)
	}

	req.AddCookie(cookie)

	resp, err = s.client.Do(req)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to get AJAX status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return producer.Message{}, fmt.Errorf("AJAX status failed with status %d", resp.StatusCode)
	}

	// Parse the XML response and extract the IP address.
	// The response is expected to be in the following format:
	//
	// <?xml version="1.0" ?>
	// <devicemap> <wan>2</wan>
	// <wan>wan0_ipaddr=1.1.1.1</wan>
	// <wan>wan1_ipaddr=</wan>
	// </devicemap>

	type deviceMap struct {
		XMLName xml.Name `xml:"devicemap"`
		WANs    []string `xml:"wan"`
	}

	var dm deviceMap
	if err := xml.NewDecoder(resp.Body).Decode(&dm); err != nil {
		return producer.Message{}, fmt.Errorf("failed to decode AJAX status XML: %w", err)
	}

	var ip string
	for _, wan := range dm.WANs {
		after, found := strings.CutPrefix(wan, s.ajaxStatusIPKey)
		if found {
			ip = after
			break
		}
	}
	if ip == "" {
		return producer.Message{}, fmt.Errorf("value for key %q not found in AJAX status XML", s.ajaxStatusIPKey)
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return producer.Message{}, fmt.Errorf("failed to parse IP address %q: %w", ip, err)
	}
	if !addr.Is4() {
		return producer.Message{}, fmt.Errorf("not an IPv4 address: %q", ip)
	}

	return producer.Message{IPv4: addr}, nil
}

// ProducerConfig contains configuration options for the ASUS router producer.
type ProducerConfig struct {
	// BaseURL is the base URL of the ASUS router web interface.
	// If empty, it defaults to "http://router.asus.com".
	BaseURL string `json:"base_url"`

	// Username is the username for logging into the ASUS router.
	Username string `json:"username"`

	// Password is the password for logging into the ASUS router.
	Password string `json:"password"`

	// AJAXStatusIPKey is the key for extracting the WAN IP address from the AJAX status response.
	// If empty, it defaults to "wan0_ipaddr=".
	AJAXStatusIPKey string `json:"ajax_status_ip_key"`

	// PollInterval is the interval between polling the router for the WAN IP address.
	// If not positive, it defaults to 5 minutes.
	PollInterval jsonhelper.Duration `json:"poll_interval"`
}

// NewProducer creates a new [producer.Producer] that monitors the WAN IP address of an ASUS router.
//
// If client is nil, [http.DefaultClient] is used.
func (cfg *ProducerConfig) NewProducer(client *http.Client) (producer.Producer, error) {
	source, err := NewSource(client, cfg.BaseURL, cfg.Username, cfg.Password, cfg.AJAXStatusIPKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create source: %w", err)
	}

	broadcaster := broadcaster.New()

	pollInterval := cfg.PollInterval.Value()
	if pollInterval <= 0 {
		pollInterval = 5 * time.Minute
	}

	return poller.New(pollInterval, source, broadcaster), nil
}
