// Package bwt wraps the Bing Webmaster Tools API with the bits a CLI needs:
// site-URL normalisation, the /Date(...)/ and {"d":...} quirks of its WCF JSON
// layer, throttling, and errors that say what to do about them.
package bwt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is the JSON flavour of the API. The same service also speaks
// POX and SOAP; only this one is worth using.
const DefaultEndpoint = "https://ssl.bing.com/webmaster/api.svc/json"

// Defaults for the transport. The API throttles aggressively and answers with
// ErrorCode 4/5 rather than HTTP 429, so the client paces itself.
const (
	DefaultTimeout = 60 * time.Second
	// DefaultThrottle is the minimum gap between requests, about 5 per second.
	DefaultThrottle   = 200 * time.Millisecond
	defaultMaxRetries = 3
)

// Client is an authenticated Bing Webmaster Tools client.
type Client struct {
	http      *http.Client
	endpoint  string
	apiKey    string
	userAgent string

	// minGap throttles outgoing requests. mu/last implement it.
	minGap     time.Duration
	maxRetries int
	mu         sync.Mutex
	last       time.Time
}

// Option customises a Client.
type Option func(*Client)

// WithEndpoint overrides the API host. It exists for tests and debugging.
func WithEndpoint(e string) Option {
	return func(c *Client) {
		if e != "" {
			c.endpoint = strings.TrimSuffix(e, "/")
		}
	}
}

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.http.Timeout = d
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithThrottle sets the minimum gap between requests. Zero disables throttling,
// which is only sensible against a local stub.
func WithThrottle(d time.Duration) Option { return func(c *Client) { c.minGap = d } }

// New builds a client for an API key.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		http:       &http.Client{Timeout: DefaultTimeout},
		endpoint:   DefaultEndpoint,
		apiKey:     apiKey,
		userAgent:  "bwt",
		minGap:     DefaultThrottle,
		maxRetries: defaultMaxRetries,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ---- transport ----

// Error is an API-level failure. Bing answers a rejected call with an HTTP 4xx
// or 5xx whose body carries its own ErrorCode, which is more specific than the
// status: a throttle and a bad parameter can both arrive as a 400.
type Error struct {
	Status  int
	Code    int
	Message string
	Body    string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Body
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if hint := e.hint(); hint != "" {
		return fmt.Sprintf("%s (HTTP %d%s)\n\n%s", msg, e.Status, codeSuffix(e.Code), hint)
	}
	return fmt.Sprintf("%s (HTTP %d%s)", msg, e.Status, codeSuffix(e.Code))
}

func codeSuffix(code int) string {
	if code == 0 {
		return ""
	}
	name := errorCodeNames[code]
	if name == "" {
		return ", ErrorCode " + strconv.Itoa(code)
	}
	return fmt.Sprintf(", ErrorCode %d %s", code, name)
}

// Bing's documented ErrorCode enum.
var errorCodeNames = map[int]string{
	1:  "InternalError",
	2:  "UnknownError",
	3:  "InvalidApiKey",
	4:  "ThrottleUser",
	5:  "ThrottleHost",
	6:  "UserBlocked",
	7:  "InvalidUrl",
	8:  "InvalidParameter",
	9:  "TooManySites",
	10: "UserNotFound",
	11: "NotFound",
	12: "AlreadyExists",
	13: "NotAllowed",
	14: "NotAuthorized",
	15: "UnexpectedState",
	16: "Deprecated",
}

func (e *Error) hint() string {
	switch e.Code {
	case 3:
		return `API キーが無効です。Bing Webmaster Tools の「設定 > API アクセス > API キー」で
発行し直し、bwt auth set で登録してください。`
	case 4, 5:
		return "スロットリングされています。少し間を空けて再実行するか、--throttle を大きくしてください。"
	case 7:
		return "URL が不正です。スキーム付きの完全な URL（https://example.com/page）で渡してください。"
	case 11:
		return "対象が見つかりません。サイトの文字列が正確かを bwt sites list で確認してください（末尾スラッシュまで含めて一致する必要があります）。"
	case 13, 14:
		return `このサイトに対する権限がありません。

  - サイト文字列が正確か確認する: bwt sites list
    （Bing のサイトは "https://example.com/" のように末尾スラッシュまで含めて1つ）
  - そのサイトが API キーの所有者アカウントで「確認済み」になっているか確認する`
	}
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "API キーかサイトの権限を確認してください（bwt auth status / bwt sites list）。"
	case http.StatusTooManyRequests:
		return "レート制限に達しました。間隔を空けて再実行してください。"
	}
	return ""
}

// retryable reports whether re-sending the same request could succeed.
func (e *Error) retryable() bool {
	return e.Status >= 500 || e.Status == http.StatusTooManyRequests || e.Code == 4 || e.Code == 5
}

// get calls a read method. Params are sent in the query string.
func (c *Client) get(ctx context.Context, method string, params url.Values, out any) error {
	return c.do(ctx, http.MethodGet, method, params, nil, out)
}

// post calls a write method. Bing wants the payload as a JSON object in the
// body while the API key stays in the query string.
func (c *Client) post(ctx context.Context, method string, body any, out any) error {
	return c.do(ctx, http.MethodPost, method, nil, body, out)
}

func (c *Client) do(ctx context.Context, httpMethod, apiMethod string, params url.Values, body, out any) error {
	if c.apiKey == "" {
		return errors.New("API キーが設定されていません")
	}

	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("apikey", c.apiKey)
	endpoint := c.endpoint + "/" + apiMethod + "?" + q.Encode()

	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}

	for attempt := 0; ; attempt++ {
		c.wait(ctx)

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, httpMethod, endpoint, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		err = c.roundTrip(req, out)
		if err == nil {
			return nil
		}

		var apiErr *Error
		if attempt >= c.maxRetries || !errors.As(err, &apiErr) || !apiErr.retryable() {
			return err
		}
		// 1s, 2s, 4s.
		select {
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) roundTrip(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return parseError(resp.StatusCode, data)
	}

	if out == nil {
		return nil
	}
	// Every successful response is wrapped in a single "d" member; write calls
	// return {"d":null}.
	var env struct {
		D json.RawMessage `json:"d"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("API のレスポンスを JSON として解釈できません: %w\n%s", err, truncate(string(data), 400))
	}
	if len(env.D) == 0 || string(env.D) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.D, out); err != nil {
		return fmt.Errorf("API のレスポンスを解釈できません: %w\n%s", err, truncate(string(env.D), 400))
	}
	return nil
}

// parseError pulls Bing's ErrorCode/Message out of an error body. The body is
// sometimes a bare JSON object and sometimes a JSON-encoded string containing
// one, so both are tried before falling back to the raw text.
func parseError(status int, data []byte) error {
	e := &Error{Status: status, Body: truncate(strings.TrimSpace(string(data)), 400)}

	var probe struct {
		ErrorCode int
		Message   string
	}
	if json.Unmarshal(data, &probe) == nil && (probe.ErrorCode != 0 || probe.Message != "") {
		e.Code, e.Message = probe.ErrorCode, probe.Message
		return e
	}
	var nested string
	if json.Unmarshal(data, &nested) == nil {
		if json.Unmarshal([]byte(nested), &probe) == nil {
			e.Code, e.Message = probe.ErrorCode, probe.Message
		} else {
			e.Message = nested
		}
	}
	return e
}

// wait spaces requests out so a loop over many URLs does not get throttled.
func (c *Client) wait(ctx context.Context) {
	if c.minGap <= 0 {
		return
	}
	c.mu.Lock()
	gap := time.Until(c.last.Add(c.minGap))
	if gap < 0 {
		gap = 0
	}
	c.last = time.Now().Add(gap)
	c.mu.Unlock()

	if gap > 0 {
		select {
		case <-time.After(gap):
		case <-ctx.Done():
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
