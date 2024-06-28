package httphelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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

	req.Header.Set("Content-Type", "application/json")
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

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}
