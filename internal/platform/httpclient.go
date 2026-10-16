package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// HTTPClient is the polite client adapters share: one request per Interval,
// retries with backoff on network errors and 429/5xx (honouring Retry-After),
// a response size cap, and redirects only within AllowedHost over https.
type HTTPClient struct {
	Header      http.Header
	Interval    time.Duration // minimum gap between requests (CzzEdit: 1 s)
	Attempts    int           // total tries per request (CzzEdit: 5)
	MaxBytes    int64         // larger responses are rejected (CzzEdit: 8 MB)
	MaxWait     time.Duration // cap for Retry-After (CzzEdit: 300 s)
	Timeout     time.Duration // per request (CzzEdit: 20 s)
	AllowedHost string        // redirect targets must be https://AllowedHost

	once   sync.Once
	client *http.Client
	mu     sync.Mutex
	last   time.Time
	// Sleep waits or returns ctx.Err(); tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Response is a completed request.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

func (c *HTTPClient) init() {
	c.once.Do(func() {
		c.client = &http.Client{
			Timeout: c.Timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" || req.URL.Hostname() != c.AllowedHost {
					return domain.NewUserError("REDIRECT", "예상하지 못한 주소로 이동하여 요청을 중단했습니다.")
				}
				return nil
			},
		}
		if c.Sleep == nil {
			c.Sleep = sleepCtx
		}
	})
}

// Get fetches u. It returns the final response for any non-retryable status;
// mapping statuses to errors is the adapter's job. Context cancellation is
// returned as ctx.Err().
func (c *HTTPClient) Get(ctx context.Context, u string) (*Response, error) {
	c.init()
	for attempt := 0; attempt < c.Attempts; attempt++ {
		if err := c.pace(ctx); err != nil {
			return nil, err
		}
		res, err := c.do(ctx, u)
		last := attempt == c.Attempts-1
		if err != nil {
			var ue *domain.UserError
			if ctx.Err() != nil || errors.As(err, &ue) {
				return nil, firstNonNil(ctx.Err(), err)
			}
			if last {
				return nil, domain.WrapUserError("NETWORK", "네트워크 요청에 실패했습니다. 연결을 확인한 뒤 이어받기를 눌러 주세요.", err)
			}
			if err := c.Sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		switch res.Status {
		case http.StatusTooManyRequests, 500, 502, 503, 504:
			if last {
				return nil, domain.NewUserError("RETRY_EXHAUSTED", "서버 응답이 지연됩니다. 잠시 후 이어받아 주세요.")
			}
			if err := c.Sleep(ctx, c.retryAfter(res.Header.Get("Retry-After"), attempt)); err != nil {
				return nil, err
			}
			continue
		}
		return res, nil
	}
	return nil, fmt.Errorf("http: no attempts configured")
}

func (c *HTTPClient) pace(ctx context.Context) error {
	c.mu.Lock()
	wait := c.Interval - time.Since(c.last)
	c.mu.Unlock()
	if wait > 0 {
		if err := c.Sleep(ctx, wait); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *HTTPClient) do(ctx context.Context, u string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range c.Header {
		req.Header[k] = v
	}
	resp, err := c.client.Do(req)
	if err != nil {
		var ue *domain.UserError
		if errors.As(err, &ue) { // redirect refused
			return nil, ue
		}
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.MaxBytes {
		return nil, domain.NewUserError("RESPONSE_TOO_LARGE", "응답 크기가 예상 범위를 넘었습니다.")
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}

func (c *HTTPClient) retryAfter(v string, attempt int) time.Duration {
	if v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(secs) && !math.IsInf(secs, 0) {
			return min(max(0, time.Duration(secs*float64(time.Second))), c.MaxWait)
		}
		if at, err := http.ParseTime(v); err == nil {
			return min(max(0, time.Until(at)), c.MaxWait)
		}
	}
	return backoff(attempt)
}

func backoff(attempt int) time.Duration { return time.Duration(1<<attempt) * time.Second }

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func firstNonNil(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// QueryURL joins base, path and query safely.
func QueryURL(base, path string, q url.Values) string {
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}
