package etl

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// The point of the progress log is that it arrives while the transfer is still
// running: one line at the end would say nothing a completed download does not
// already say.
func TestProgressReaderReportsBeforeTheTransferEnds(t *testing.T) {
	var reports []int64
	reader := &progressReader{
		reader: strings.NewReader("abcdef"),
		total:  6,
		report: func(read, total int64) {
			if total != 6 {
				t.Errorf("report carried total %d, want 6", total)
			}
			reports = append(reports, read)
		},
	}

	drain(t, reader, 2)

	if len(reports) == 0 {
		t.Fatal("the reader reported nothing")
	}
	if first := reports[0]; first != 2 {
		t.Errorf("first report was at %d bytes, want 2: progress arrived late", first)
	}
	if last := reports[len(reports)-1]; last != 6 {
		t.Errorf("last report was at %d bytes, want 6", last)
	}
}

// A transfer fast enough to finish inside one interval should say nothing at
// all, or every short run would carry a line nobody needs.
func TestProgressReaderStaysQuietWithinItsInterval(t *testing.T) {
	reported := false
	reader := &progressReader{
		reader:     strings.NewReader("abcdef"),
		total:      6,
		interval:   time.Hour,
		lastReport: time.Now(),
		report:     func(read, total int64) { reported = true },
	}

	drain(t, reader, 2)

	if reported {
		t.Error("the reader reported inside its own interval")
	}
}

func drain(t *testing.T, reader io.Reader, chunk int) {
	t.Helper()

	buffer := make([]byte, chunk)
	for {
		switch _, err := reader.Read(buffer); {
		case errors.Is(err, io.EOF):
			return
		case err != nil:
			t.Fatalf("reading: %v", err)
		}
	}
}
