package etl

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveScreenshot(t *testing.T, contentType string) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if _, err := w.Write([]byte("image bytes")); err != nil {
			t.Errorf("writing the response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

func TestFetchScreenshotKeepsARasterImage(t *testing.T) {
	upstream := serveScreenshot(t, "image/png; charset=binary")

	image, contentType, err := fetchScreenshot(t.Context(), upstream.Client(), upstream.URL, "test", quietLogger())
	if err != nil {
		t.Fatalf("fetchScreenshot: %v", err)
	}
	if contentType != "image/png" {
		t.Errorf("content type = %q, want image/png", contentType)
	}
	if string(image) != "image bytes" {
		t.Errorf("image = %q, want the served bytes", image)
	}
}

func TestFetchScreenshotRejectsWhatCouldCarryScript(t *testing.T) {
	for _, contentType := range []string{"image/svg+xml", "text/html", "image/png;;", ""} {
		upstream := serveScreenshot(t, contentType)
		if _, _, err := fetchScreenshot(t.Context(), upstream.Client(), upstream.URL, "test", quietLogger()); err == nil {
			t.Errorf("a screenshot served as %q should be rejected", contentType)
		}
	}
}
