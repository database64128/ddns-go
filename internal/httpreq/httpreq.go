package httpreq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	ddnsgo "github.com/database64128/ddns-go"
)

// DefaultUserAgent is the default User-Agent header value used in HTTP requests.
const DefaultUserAgent = "cubic-ddns-go/" + ddnsgo.Version

// NewJSONRequest creates a new HTTP request with the given
// method, url, and body encoded as JSON. Content-Type is
// set to application/json.
func NewJSONRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	switch b := body.(type) {
	case io.Reader:
		bodyReader = b
	case []byte:
		bodyReader = bytes.NewReader(b)
	case string:
		bodyReader = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["Content-Type"] = []string{"application/json"}
	return req, nil
}

// NewFormRequest creates a new HTTP request with the given
// method, url, and values encoded as form. Content-Type is
// set to application/x-www-form-urlencoded.
func NewFormRequest(ctx context.Context, method, url string, values url.Values) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header["Content-Type"] = []string{"application/x-www-form-urlencoded"}
	return req, nil
}

// ReadResponseBody reads up to maxSize bytes from the response body into buf.
// If the size of the response body exceeds maxSize, an error is returned.
func ReadResponseBody(buf *bytes.Buffer, resp *http.Response, maxSize int64) error {
	if resp.ContentLength > maxSize {
		return fmt.Errorf("response body too large: %d bytes (max %d bytes)", resp.ContentLength, maxSize)
	}
	buf.Grow(max(0, int(resp.ContentLength)))
	r := io.LimitReader(resp.Body, maxSize+1)
	n, err := io.Copy(buf, r)
	if err != nil {
		return err
	}
	if n > maxSize {
		return fmt.Errorf("response body too large: %d bytes (max %d bytes)", n, maxSize)
	}
	return nil
}

// DefaultHttpTransportClone returns a clone of [http.DefaultTransport] if possible,
// or a best-effort approximation of the original if it has been changed by user code
// to a custom implementation.
func DefaultHttpTransportClone() *http.Transport {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		dialer := net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	return transport.Clone()
}
