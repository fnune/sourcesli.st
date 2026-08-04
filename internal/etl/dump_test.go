package etl

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A 304 means the previously published data is still current in Postgres. The
// cached dump file plays no part in that, so its absence (an ephemeral cache
// directory) must not turn an unchanged upstream into a failed run.
func TestFetchTreatsNotModifiedAsUnchangedWithoutACachedFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "" {
			t.Error("the request carried no If-None-Match header")
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer upstream.Close()

	source := &DumpSource{
		client:    upstream.Client(),
		url:       upstream.URL,
		cacheDir:  t.TempDir(),
		userAgent: "sourcesli.st/test (+https://github.com/fnune/sourcesli.st)",
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	_, _, err := source.fetch(t.Context(), UpstreamState{ETag: `"abc"`})
	if !errors.Is(err, ErrUnchanged) {
		t.Fatalf("fetch returned %v, want ErrUnchanged", err)
	}
}
