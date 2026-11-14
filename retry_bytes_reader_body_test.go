package openai_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// A transport may keep reading a request body after it has returned a
// response, for example when a server answers 503 before consuming a large
// upload. Each retry must then send its own complete copy of a *bytes.Reader
// body instead of rewinding and sharing the reader of the previous attempt.
func TestRetryResendsCompleteBytesReaderBodyWhilePreviousAttemptReads(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	var bodies []io.ReadCloser
	var retried []byte
	client := openai.NewClient(
		option.WithAPIKey("My API Key"),
		option.WithMaxRetries(1),
		option.WithHTTPClient(&http.Client{
			Transport: retryDelayRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				bodies = append(bodies, req.Body)
				if len(bodies) == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Header:     http.Header{"Retry-After-Ms": []string{"0"}},
						Body:       http.NoBody,
					}, nil
				}
				// The first attempt's writer is still running.
				_, _ = io.CopyN(io.Discard, bodies[0], 1024)
				var err error
				retried, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			}),
		}),
	)

	var out map[string]any
	if err := client.Post(context.Background(), "uploads/raw", bytes.NewReader(payload), &out,
		option.WithHeader("Content-Type", "application/octet-stream")); err != nil {
		t.Fatalf("Post error = %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("attempts = %d, want 2", len(bodies))
	}
	if !bytes.Equal(retried, payload) {
		t.Fatalf("retried body has %d bytes, want the complete %d-byte payload", len(retried), len(payload))
	}
}

// A retry must resend the same bytes as the first attempt, which start at the
// reader's position when the request was made, not at offset zero.
func TestRetryResendsBytesReaderBodyFromItsStartingOffset(t *testing.T) {
	reader := bytes.NewReader([]byte("skipped-prefix|payload"))
	if _, err := reader.Seek(int64(len("skipped-prefix|")), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var sent []string
	var lengths []int64
	client := openai.NewClient(
		option.WithAPIKey("My API Key"),
		option.WithMaxRetries(1),
		option.WithHTTPClient(&http.Client{
			Transport: retryDelayRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				sent = append(sent, string(body))
				lengths = append(lengths, req.ContentLength)
				if len(sent) == 1 {
					return &http.Response{
						StatusCode: http.StatusServiceUnavailable,
						Header:     http.Header{"Retry-After-Ms": []string{"0"}},
						Body:       http.NoBody,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			}),
		}),
	)

	var out map[string]any
	if err := client.Post(context.Background(), "uploads/raw", reader, &out,
		option.WithHeader("Content-Type", "application/octet-stream")); err != nil {
		t.Fatalf("Post error = %v", err)
	}
	if len(sent) != 2 || sent[0] != "payload" || sent[1] != "payload" {
		t.Fatalf("sent bodies = %q, want the remaining payload on both attempts", sent)
	}
	for i, length := range lengths {
		if length != int64(len("payload")) {
			t.Fatalf("attempt %d ContentLength = %d, want %d", i+1, length, len("payload"))
		}
	}
}
