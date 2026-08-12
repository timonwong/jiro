package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/timonwong/jiro/internal/apperr"
)

const defaultUserAgent = "jiro/dev"

// Config configures a Client. PAT uses Jira's Bearer authentication; Basic
// authentication is used when Username and Password are supplied.
type Config struct {
	BaseURL    string
	Username   string
	Password   string
	PAT        string
	UserAgent  string
	HTTPClient *http.Client
}

// HTTPError preserves an unsuccessful HTTP response for callers that need
// Jira's original response body.
type HTTPError struct {
	StatusCode int
	Body       []byte
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("jira API returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("jira API returned HTTP %d", e.StatusCode)
}

// Client talks to Jira REST API v2.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	username   string
	password   string
	pat        string
	userAgent  string
}

// NewClient constructs a client. It accepts a base URL with or without a
// trailing slash and preserves a context path such as /jira.
func NewClient(cfg Config) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		if err == nil {
			err = fmt.Errorf("base URL must include scheme and host")
		}
		return nil, apperr.Wrap(apperr.KindConfig, err, "invalid Jira base URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, apperr.New(apperr.KindConfig, "Jira base URL must use http or https")
	}
	base.RawQuery = ""
	base.Fragment = ""
	base.Path = strings.TrimRight(base.Path, "/")

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	return &Client{
		baseURL: base, httpClient: client, username: cfg.Username, password: cfg.Password,
		pat: cfg.PAT, userAgent: userAgent,
	}, nil
}

func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(path, "/")
	u.RawQuery = query.Encode()
	return u.String()
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, input, output any) error {
	raw, err := c.RawRequest(ctx, method, path, query, input)
	if err != nil || output == nil || len(raw) == 0 {
		return err
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return apperr.Wrap(apperr.KindAPI, err, "decode Jira API response")
	}
	return nil
}

// RawRequest sends an authenticated API request and returns its unmodified
// successful response body. It uses the same status-error mapping as typed
// operations; path must be relative to the configured Jira base URL.
func (c *Client) RawRequest(ctx context.Context, method, path string, query url.Values, input any) ([]byte, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, apperr.Wrap(apperr.KindInvalidInput, err, "encode Jira request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query), body)
	if err != nil {
		return nil, apperr.Wrap(apperr.KindInvalidInput, err, "create Jira request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.pat != "" {
		req.Header.Set("Authorization", "Bearer "+c.pat)
	} else if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, apperr.Wrap(apperr.KindAPI, err, "request Jira API")
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, apperr.Wrap(apperr.KindAPI, readErr, "read Jira API response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		httpErr := &HTTPError{StatusCode: resp.StatusCode, Body: raw, Message: jiraErrorMessage(raw)}
		kind := ClassifyStatus(resp.StatusCode)
		return nil, &apperr.Error{Kind: kind, Message: httpErr.Error(), StatusCode: resp.StatusCode, Cause: httpErr}
	}
	return raw, nil
}

// ClassifyStatus maps an HTTP response status code to the apperr.Kind used
// for both typed operations and the api command's raw streaming path, so the
// two error paths always agree on exit codes for identical Jira failures.
func ClassifyStatus(statusCode int) apperr.Kind {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return apperr.KindAuth
	case http.StatusNotFound:
		return apperr.KindNotFound
	case http.StatusTooManyRequests:
		return apperr.KindRateLimit
	default:
		return apperr.KindAPI
	}
}

func jiraErrorMessage(raw []byte) string {
	var payload struct {
		ErrorMessages []string       `json:"errorMessages"`
		Errors        map[string]any `json:"errors"`
		Message       string         `json:"message"`
		Error         string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	parts := append([]string(nil), payload.ErrorMessages...)
	if payload.Message != "" {
		parts = append(parts, payload.Message)
	}
	if payload.Error != "" {
		parts = append(parts, payload.Error)
	}
	keys := make([]string, 0, len(payload.Errors))
	for key := range payload.Errors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", key, payload.Errors[key]))
	}
	return strings.Join(parts, "; ")
}
