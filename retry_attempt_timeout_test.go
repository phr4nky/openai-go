package openai_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// The wait before a retry must not be charged to the per-attempt
// WithRequestTimeout deadline: a Retry-After longer than the remaining attempt
// time previously ended the call with context.DeadlineExceeded after one
// attempt, dropping both the retry and the server's *openai.Error.
func TestRetryAfterLongerThanRequestTimeoutStillRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		client := openai.NewClient(
			option.WithAPIKey("My API Key"),
			option.WithMaxRetries(1),
			option.WithRequestTimeout(time.Second),
			option.WithHTTPClient(&http.Client{
				Transport: retryDelayRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
					attempts++
					if attempts == 1 {
						return &http.Response{
							StatusCode: http.StatusTooManyRequests,
							Header:     http.Header{"Retry-After": []string{"2"}},
							Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"m","object":"model","created":1,"owned_by":"o"}`)),
					}, nil
				}),
			}),
		)

		model, err := client.Models.Get(context.Background(), "m")
		if err != nil {
			t.Fatalf("Models.Get error = %v, want retry to succeed", err)
		}
		if attempts != 2 || model.ID != "m" {
			t.Fatalf("attempts = %d, model = %q; want 2 attempts and model m", attempts, model.ID)
		}
	})
}

// When retries are exhausted the caller must see the server's error, not the
// deadline of the attempt that preceded the wait.
func TestRetryWaitDoesNotReplaceAPIErrorWithAttemptDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		client := openai.NewClient(
			option.WithAPIKey("My API Key"),
			option.WithMaxRetries(1),
			option.WithRequestTimeout(time.Second),
			option.WithHTTPClient(&http.Client{
				Transport: retryDelayRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
					attempts++
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Header:     http.Header{"Retry-After": []string{"2"}},
						Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
					}, nil
				}),
			}),
		)

		_, err := client.Models.Get(context.Background(), "m")
		var apiErr *openai.Error
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("error = %v, want *openai.Error with status 429", err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, must not be the attempt deadline", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
	})
}

// The caller's own context still bounds the wait between attempts.
func TestRetryWaitHonorsCallerContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		client := openai.NewClient(
			option.WithAPIKey("My API Key"),
			option.WithMaxRetries(1),
			option.WithRequestTimeout(time.Second),
			option.WithHTTPClient(&http.Client{
				Transport: retryDelayRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
					attempts++
					return &http.Response{
						StatusCode: http.StatusTooManyRequests,
						Header:     http.Header{"Retry-After": []string{"10"}},
						Body:       http.NoBody,
					}, nil
				}),
			}),
		)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		_, err := client.Models.Get(ctx, "m")
		if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
			t.Fatalf("error = %v, attempts = %d; want caller deadline after 1 attempt", err, attempts)
		}
	})
}
