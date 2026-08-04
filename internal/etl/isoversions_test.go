package etl

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchISOVersionParsesTheChecksumFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(
			"abc123  debian-13.6.0-amd64-netinst.iso\n" +
				"def456  debian-edu-13.6.0-amd64-netinst.iso\n",
		)); err != nil {
			t.Errorf("writing the response: %v", err)
		}
	}))
	defer upstream.Close()

	version, err := fetchISOVersion(t.Context(), upstream.Client(), upstream.URL, "test", quietLogger())
	if err != nil {
		t.Fatalf("fetchISOVersion: %v", err)
	}
	if version != "13.6.0" {
		t.Errorf("version = %q, want 13.6.0", version)
	}
}

func TestFetchISOVersionRejectsAFileWithoutVersions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("<html>not a checksum file</html>")); err != nil {
			t.Errorf("writing the response: %v", err)
		}
	}))
	defer upstream.Close()

	if _, err := fetchISOVersion(t.Context(), upstream.Client(), upstream.URL, "test", quietLogger()); err == nil {
		t.Fatal("a response naming no debian-<version> file should be an error")
	}
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
