package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	deixicServicePath = "/deixicpublic.v1.DeixicPublicService/"
	maxResponseBytes  = 4 << 20
)

var ErrNotFound = errors.New("deixic resource not found")

type Client struct {
	endpoint       string
	token          string
	organizationID string
	workspaceID    string
	httpClient     *http.Client
}

type APIError struct {
	StatusCode       int
	Code             string
	Message          string
	CanonicalConnect bool
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Deixic API returned %s (HTTP %d)", e.Code, e.StatusCode)
	}
	return fmt.Sprintf("Deixic API returned %s (HTTP %d): %s", e.Code, e.StatusCode, e.Message)
}

func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.CanonicalConnect && e.StatusCode == http.StatusNotFound && e.Code == "not_found"
}

func NewClient(endpoint, token, organizationID, workspaceID string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("endpoint must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return nil, errors.New("endpoint must not include user information")
	}
	if parsed.Scheme == "http" && !isLoopbackHostname(parsed.Hostname()) {
		return nil, errors.New("http endpoints are allowed only for loopback development servers; use https")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("endpoint must not include a query string or fragment")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	httpClientCopy := *httpClient
	httpClientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		endpoint:       strings.TrimRight(parsed.String(), "/"),
		token:          token,
		organizationID: organizationID,
		workspaceID:    workspaceID,
		httpClient:     &httpClientCopy,
	}, nil
}

func isLoopbackHostname(hostname string) bool {
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

func (c *Client) OrganizationID() string { return c.organizationID }
func (c *Client) WorkspaceID() string    { return c.workspaceID }
func (c *Client) Endpoint() string       { return c.endpoint }

func (c *Client) Invoke(ctx context.Context, method string, request, response proto.Message) error {
	body, err := proto.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint+deixicServicePath+method,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	httpRequest.Header.Set("Content-Type", "application/proto")
	httpRequest.Header.Set("Accept", "application/proto")
	httpRequest.Header.Set("Connect-Protocol-Version", "1")
	httpRequest.Header.Set("X-Organization-Id", c.organizationID)
	httpRequest.Header.Set("X-Workspace-Id", c.workspaceID)
	httpRequest.Header.Set("User-Agent", "terraform-provider-deixic")

	httpResponse, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("call Deixic %s: %w", method, err)
	}
	defer func() { _ = httpResponse.Body.Close() }()

	responseBody, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Deixic %s response: %w", method, err)
	}
	if len(responseBody) > maxResponseBytes {
		return fmt.Errorf("deixic %s response exceeds %d bytes", method, maxResponseBytes)
	}
	if httpResponse.StatusCode != http.StatusOK {
		return decodeAPIError(
			httpResponse.StatusCode,
			httpResponse.Header.Get("Content-Type"),
			httpResponse.Header.Get("Connect-Protocol-Version"),
			responseBody,
		)
	}
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(httpResponse.Header.Get("Content-Type"), ";")[0])); mediaType != "application/proto" {
		return fmt.Errorf("deixic %s returned unexpected Content-Type %q", method, httpResponse.Header.Get("Content-Type"))
	}
	if protocolVersion := httpResponse.Header.Get("Connect-Protocol-Version"); protocolVersion != "1" {
		return fmt.Errorf("deixic %s returned unexpected Connect-Protocol-Version %q", method, protocolVersion)
	}
	if err := proto.Unmarshal(responseBody, response); err != nil {
		return fmt.Errorf("decode Deixic %s response: %w", method, err)
	}
	return nil
}

func decodeAPIError(statusCode int, contentType, protocolVersion string, body []byte) error {
	payload := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	canonicalConnect := mediaType == "application/json" && protocolVersion == "1"
	if err := json.Unmarshal(body, &payload); err != nil || payload.Code == "" || payload.Message == "" {
		canonicalConnect = false
		payload.Code = http.StatusText(statusCode)
		payload.Message = "unexpected non-Connect error response"
	}
	if payload.Code == "" {
		payload.Code = http.StatusText(statusCode)
	}
	return &APIError{
		StatusCode:       statusCode,
		Code:             payload.Code,
		Message:          payload.Message,
		CanonicalConnect: canonicalConnect,
	}
}
