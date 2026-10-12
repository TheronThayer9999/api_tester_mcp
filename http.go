package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultUserAgent  = "apiprobe-mcp/1.0"
	defaultTimeoutMS  = 10_000
	maxTimeoutMS      = 300_000
	maxBodyBytes    = 64 * 1024
	maxExcerptRunes = 1000
)

var httpClient = &http.Client{}

// requestInput is the shared shape of an HTTP call across tools.
type requestInput struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Query       map[string]string `json:"query"`
	Body        string            `json:"body"`
	TimeoutMS   int               `json:"timeout_ms"`
	AuthProfile string            `json:"auth_profile"`
}

type responseOutput struct {
	Status      int               `json:"status"`
	StatusText  string            `json:"status_text"`
	DurationMS  int64             `json:"duration_ms"`
	SizeBytes   int               `json:"size_bytes"`
	Truncated   bool              `json:"truncated"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	AppliedAuth []string          `json:"applied_auth,omitempty"`
}

func (in *requestInput) normalize() error {
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	if in.Method == "" {
		in.Method = http.MethodGet
	}
	if strings.TrimSpace(in.URL) == "" {
		return fmt.Errorf("url is required")
	}
	return nil
}

// do executes the request and returns the response view plus the raw body
// bytes (needed for JSON assertions).
func (in *requestInput) do(ctx context.Context) (*responseOutput, []byte, error) {
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		return nil, nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, fmt.Errorf("unsupported url scheme %q (want http or https)", u.Scheme)
	}
	if len(in.Query) > 0 {
		q := u.Query()
		for k, v := range in.Query {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
	}

	var body io.Reader
	if in.Body != "" {
		body = strings.NewReader(in.Body)
	}
	req, err := http.NewRequestWithContext(ctx, in.Method, u.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}

	appliedAuth, err := auth.apply(req, in.AuthProfile)
	if err != nil {
		return nil, nil, err
	}

	timeout := in.TimeoutMS
	if timeout <= 0 {
		timeout = defaultTimeoutMS
	}
	if timeout > maxTimeoutMS {
		timeout = maxTimeoutMS
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()

	start := time.Now()
	resp, err := httpClient.Do(req.WithContext(reqCtx))
	if err != nil {
		return nil, nil, fmt.Errorf("request to %s failed after %s: %w",
			u.Redacted(), time.Since(start).Round(time.Millisecond), err)
	}
	defer resp.Body.Close()

	raw, truncated, err := readLimited(resp.Body, maxBodyBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	out := &responseOutput{
		Status:      resp.StatusCode,
		StatusText:  resp.Status,
		DurationMS:  time.Since(start).Milliseconds(),
		SizeBytes:   len(raw),
		Truncated:   truncated,
		Headers:     flattenHeaders(resp.Header),
		Body:        strings.ToValidUTF8(string(raw), ""),
		AppliedAuth: appliedAuth,
	}
	return out, raw, nil
}

func readLimited(r io.Reader, limit int) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

func httpRequestTool() mcp.Tool {
	return mcp.NewTool("http_request",
		mcp.WithDescription("Send an HTTP request and get back status, headers, body and timing. Use this to probe an endpoint; use api_test when you need pass/fail assertions."),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("HTTP method"),
			mcp.Enum("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"),
		),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("Absolute http(s) URL to call"),
		),
		mcp.WithObject("headers",
			mcp.Description("Request headers as key/value pairs"),
			mcp.AdditionalProperties(map[string]any{"type": "string"}),
		),
		mcp.WithObject("query",
			mcp.Description("Query string parameters as key/value pairs"),
			mcp.AdditionalProperties(map[string]any{"type": "string"}),
		),
		mcp.WithString("body",
			mcp.Description("Request body, usually a JSON string"),
		),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Timeout in milliseconds (default 10000, max 300000)"),
		),
		mcp.WithString("auth_profile",
			mcp.Description("Name of a saved auth profile (save_auth) to attach. Omit to auto-apply profiles matching the URL."),
		),
	)
}

func handleHTTPRequest(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in requestInput
	if err := r.BindArguments(&in); err != nil {
		return mcp.NewToolResultErrorFromErr("invalid arguments", err), nil
	}
	if err := in.normalize(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	out, _, err := in.do(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(out)
}

// testInput extends requestInput with assertions.
type testInput struct {
	requestInput
	ExpectStatus   int               `json:"expect_status"`
	ExpectJSON     map[string]any    `json:"expect_json"`
	ExpectContains string            `json:"expect_contains"`
	ExpectHeader   map[string]string `json:"expect_header"`
}

type checkResult struct {
	Check    string `json:"check"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Passed   bool   `json:"passed"`
}

type testOutput struct {
	Passed      bool          `json:"passed"`
	Status      int           `json:"status"`
	DurationMS  int64         `json:"duration_ms"`
	AppliedAuth []string      `json:"applied_auth,omitempty"`
	Results     []checkResult `json:"results"`
	BodyExcerpt string        `json:"body_excerpt,omitempty"`
}

func apiTestTool() mcp.Tool {
	return mcp.NewTool("api_test",
		mcp.WithDescription("Send an HTTP request and run assertions against the response. Returns passed=true/false with a per-check breakdown. Checks: expect_status, expect_json (dot-path keys like \"data.items.0.id\"), expect_contains (body substring), expect_header (value substring)."),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("HTTP method"),
			mcp.Enum("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"),
		),
		mcp.WithString("url",
			mcp.Required(),
			mcp.Description("Absolute http(s) URL to call"),
		),
		mcp.WithObject("headers",
			mcp.Description("Request headers as key/value pairs"),
			mcp.AdditionalProperties(map[string]any{"type": "string"}),
		),
		mcp.WithObject("query",
			mcp.Description("Query string parameters as key/value pairs"),
			mcp.AdditionalProperties(map[string]any{"type": "string"}),
		),
		mcp.WithString("body",
			mcp.Description("Request body, usually a JSON string"),
		),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Timeout in milliseconds (default 10000, max 300000)"),
		),
		mcp.WithString("auth_profile",
			mcp.Description("Name of a saved auth profile (save_auth) to attach. Omit to auto-apply profiles matching the URL."),
		),
		mcp.WithNumber("expect_status",
			mcp.Description("Expected HTTP status code, e.g. 200"),
		),
		mcp.WithObject("expect_json",
			mcp.Description("Expected JSON body values keyed by dot-path (arrays indexed numerically), e.g. {\"data.id\": 1, \"items.0.ok\": true}"),
			mcp.AdditionalProperties(true),
		),
		mcp.WithString("expect_contains",
			mcp.Description("Substring that must appear in the response body"),
		),
		mcp.WithObject("expect_header",
			mcp.Description("Response header names mapped to substrings that must appear in their value"),
			mcp.AdditionalProperties(map[string]any{"type": "string"}),
		),
	)
}

func handleAPITest(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in testInput
	if err := r.BindArguments(&in); err != nil {
		return mcp.NewToolResultErrorFromErr("invalid arguments", err), nil
	}
	if err := in.normalize(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, raw, err := in.do(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := testOutput{
		Status:      resp.Status,
		DurationMS:  resp.DurationMS,
		AppliedAuth: resp.AppliedAuth,
		Results:     []checkResult{},
		Passed:      true,
	}
	add := func(cr checkResult) {
		if !cr.Passed {
			out.Passed = false
		}
		out.Results = append(out.Results, cr)
	}

	if in.ExpectStatus != 0 {
		add(checkResult{
			Check:    "status",
			Expected: in.ExpectStatus,
			Actual:   resp.Status,
			Passed:   resp.Status == in.ExpectStatus,
		})
	}

	var parsed any
	if len(in.ExpectJSON) > 0 {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			add(checkResult{
				Check:    "json_parse",
				Expected: "valid JSON body",
				Actual:   err.Error(),
				Passed:   false,
			})
		} else {
			for _, path := range slices.Sorted(maps.Keys(in.ExpectJSON)) {
				expected := in.ExpectJSON[path]
				actual, ok := lookupJSONPath(parsed, path)
				if !ok {
					add(checkResult{
						Check:    "json:" + path,
						Expected: expected,
						Actual:   "<path not found>",
						Passed:   false,
					})
					continue
				}
				add(checkResult{
					Check:    "json:" + path,
					Expected: expected,
					Actual:   actual,
					Passed:   valuesEqual(actual, expected),
				})
			}
		}
	}

	for name, want := range in.ExpectHeader {
		got := resp.Headers[http.CanonicalHeaderKey(name)]
		add(checkResult{
			Check:    "header:" + name,
			Expected: want,
			Actual:   got,
			Passed:   strings.Contains(strings.ToLower(got), strings.ToLower(want)),
		})
	}

	if in.ExpectContains != "" {
		add(checkResult{
			Check:    "body_contains",
			Expected: in.ExpectContains,
			Actual:   fmt.Sprintf("%d bytes", resp.SizeBytes),
			Passed:   strings.Contains(resp.Body, in.ExpectContains),
		})
	}

	if !out.Passed && resp.Body != "" {
		out.BodyExcerpt = truncateExcerpt(resp.Body, maxExcerptRunes)
	}
	return jsonResult(out)
}

// lookupJSONPath resolves a dot-separated path through decoded JSON,
// supporting object keys and numeric array indexes.
func lookupJSONPath(data any, path string) (any, bool) {
	cur := data
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := parseIndex(part)
			if err != nil || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

func parseIndex(s string) (int, error) {
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("leading zero index")
	}
	var idx int
	if _, err := fmt.Sscanf(s, "%d", &idx); err != nil || idx < 0 {
		return 0, fmt.Errorf("invalid index %q", s)
	}
	return idx, nil
}

func valuesEqual(actual, expected any) bool {
	return fmt.Sprint(actual) == fmt.Sprint(expected)
}

func truncateExcerpt(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "...(truncated)"
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	res, err := mcp.NewToolResultJSON(v)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("encode result", err), nil
	}
	return res, nil
}
