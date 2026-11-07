package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// Official endpoint, hardcoded so tenants cannot redirect the bearer token.
	// https://specstream.ai/docs/search-api
	specStreamSearchURL       = "https://api.specstream.ai/v1/web-search"
	defaultSpecStreamTimeout  = 30 * time.Second
	defaultSpecStreamResults  = 5
	maxSpecStreamResponseSize = 2 << 20
)

// SpecStreamProvider queries the SpecStream web search API.
type SpecStreamProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewSpecStreamProvider creates a SpecStream search client from the saved API key.
func NewSpecStreamProvider(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for SpecStream provider")
	}
	client, err := NewSearchHTTPClient(defaultSpecStreamTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// Never forward the bearer token to a redirect destination.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &SpecStreamProvider{
		client:  client,
		baseURL: specStreamSearchURL,
		apiKey:  strings.TrimSpace(params.APIKey),
	}, nil
}

// Name returns the provider registry identifier.
func (p *SpecStreamProvider) Name() string { return "specstream" }

// Search calls GET /v1/web-search. The API has no result-count parameter, so
// the rows are trimmed here.
func (p *SpecStreamProvider) Search(
	ctx context.Context, request types.WebSearchRequest,
) ([]*types.WebSearchResult, error) {
	query, maxResults, includeDate := strings.TrimSpace(request.Query), request.MaxResults, request.IncludeDate
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultSpecStreamResults
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, p.baseURL+"?"+url.Values{"q": {query}}.Encode(), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create SpecStream request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	logger.Infof(ctx, "[WebSearch][SpecStream] query=%q maxResults=%d", query, maxResults)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute SpecStream request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readCapped(resp.Body, maxSpecStreamResponseSize)
	if err != nil {
		return nil, fmt.Errorf("failed to read SpecStream response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, specStreamHTTPError(resp, body)
	}

	var response specStreamSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SpecStream response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Data.Sources)))
	for _, item := range response.Data.Sources {
		pageURL := strings.TrimSpace(item.URL)
		if pageURL == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     pageURL,
			Snippet: strings.TrimSpace(item.Snippet),
			Source:  p.Name(),
		}
		if includeDate {
			if publishedAt, ok := parseSpecStreamDate(item.Published); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][SpecStream] query=%q returned %d results", query, len(results))
	return results, nil
}

func specStreamHTTPError(resp *http.Response, body []byte) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("SpecStream API rejected the API key (HTTP 401)")
	case http.StatusPaymentRequired:
		return fmt.Errorf("SpecStream account balance is empty (HTTP 402)")
	case http.StatusServiceUnavailable:
		if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
			return fmt.Errorf("SpecStream API is unavailable (HTTP 503), retry after %s seconds", retryAfter)
		}
		return fmt.Errorf("SpecStream API is unavailable (HTTP 503)")
	}
	if detail := errorPreviewString(body); detail != "" {
		return fmt.Errorf("SpecStream API returned status %d: %s", resp.StatusCode, detail)
	}
	return fmt.Errorf("SpecStream API returned status %d", resp.StatusCode)
}

func parseSpecStreamDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type specStreamSearchResponse struct {
	Data struct {
		Sources []specStreamSource `json:"sources"`
	} `json:"data"`
}

type specStreamSource struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet"`
	Published string `json:"published"`
}
