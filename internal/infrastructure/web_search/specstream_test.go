package web_search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSpecStreamProviderSearchMapsAndTrimsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "rust & go" {
			t.Fatalf("q = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":{"kind":"answer","answer":null,"rows":[],"sources":[
			{"url":"https://example.com/1","title":"One","site":"example.com","snippet":" First ","published":"2026-06-01T00:00:00Z","cited":true},
			{"url":"","title":"Dropped","snippet":"no url"},
			{"url":"https://example.com/2","title":"Two","snippet":"Second","published":"not-a-date"},
			{"url":"https://example.com/3","title":"Three","snippet":"trimmed"}
		]}}`))
	}))
	defer server.Close()

	provider := &SpecStreamProvider{client: server.Client(), baseURL: server.URL, apiKey: "test-key"}
	results, err := provider.Search(context.Background(), types.WebSearchRequest{
		Query: " rust & go ", MaxResults: 2, IncludeDate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Title != "One" || results[0].Snippet != "First" || results[0].Source != "specstream" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC); results[0].PublishedAt == nil || !results[0].PublishedAt.Equal(want) {
		t.Fatalf("published = %v, want %v", results[0].PublishedAt, want)
	}
	if results[1].URL != "https://example.com/2" || results[1].PublishedAt != nil {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

func TestSpecStreamProviderSearchOmitsDateUnlessRequested(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"sources":[{"url":"https://example.com","published":"2026-06-01"}]}}`))
	}))
	defer server.Close()
	provider := &SpecStreamProvider{client: server.Client(), baseURL: server.URL, apiKey: "k"}
	results, err := provider.Search(context.Background(), types.WebSearchRequest{Query: "q"})
	if err != nil || len(results) != 1 || results[0].PublishedAt != nil {
		t.Fatalf("results = %+v, err = %v", results, err)
	}
}

func TestSpecStreamProviderSearchErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  string
		want   string
	}{
		{http.StatusUnauthorized, "", "HTTP 401"},
		{http.StatusPaymentRequired, "", "HTTP 402"},
		{http.StatusServiceUnavailable, "7", "retry after 7 seconds"},
		{http.StatusBadGateway, "", "status 502"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.retry != "" {
				w.Header().Set("Retry-After", tc.retry)
			}
			w.WriteHeader(tc.status)
		}))
		provider := &SpecStreamProvider{client: server.Client(), baseURL: server.URL, apiKey: "k"}
		_, err := provider.Search(context.Background(), types.WebSearchRequest{Query: "q"})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("status %d: error = %v, want %q", tc.status, err, tc.want)
		}
	}
}

func TestNewSpecStreamProviderRequiresKeyAndRefusesRedirects(t *testing.T) {
	if _, err := NewSpecStreamProvider(types.WebSearchProviderParameters{APIKey: "  "}); err == nil {
		t.Fatal("blank API key was accepted")
	}
	searcher, err := NewSpecStreamProvider(types.WebSearchProviderParameters{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	provider := searcher.(*SpecStreamProvider)
	if provider.baseURL != specStreamSearchURL {
		t.Fatalf("baseURL = %q", provider.baseURL)
	}
	if err := provider.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("CheckRedirect = %v", err)
	}
}
