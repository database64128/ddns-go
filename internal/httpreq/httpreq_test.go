package httpreq_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/database64128/ddns-go/internal/httpreq"
)

func TestReadResponseBody(t *testing.T) {
	for _, c := range [...]struct {
		name    string
		resp    http.Response
		maxSize int64
		wantLen int
		wantErr bool
	}{
		{
			name: "SuccessExactContentLength",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: 13,
			},
			maxSize: 20,
			wantLen: 13,
			wantErr: false,
		},
		{
			name: "SuccessShortContentLength",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: 5,
			},
			maxSize: 20,
			wantLen: 13,
			wantErr: false,
		},
		{
			name: "SuccessNoContentLength",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: -1,
			},
			maxSize: 20,
			wantLen: 13,
			wantErr: false,
		},
		{
			name: "SuccessExactMaxSize",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: 13,
			},
			maxSize: 13,
			wantLen: 13,
			wantErr: false,
		},
		{
			name: "ErrorContentLengthExceedsMaxSize",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: 30,
			},
			maxSize: 20,
			wantLen: 0,
			wantErr: true,
		},
		{
			name: "ErrorReadExceedsMaxSize",
			resp: http.Response{
				Body:          io.NopCloser(strings.NewReader("Hello, World!")),
				ContentLength: -1,
			},
			maxSize: 5,
			wantLen: 6,
			wantErr: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := httpreq.ReadResponseBody(&buf, &c.resp, c.maxSize)
			if (err != nil) != c.wantErr {
				t.Errorf("ReadResponseBody() error = %v, wantErr %v", err, c.wantErr)
			}
			if got := buf.Len(); got != c.wantLen {
				t.Errorf("ReadResponseBody() read %d bytes, want %d", got, c.wantLen)
			}
		})
	}
}
