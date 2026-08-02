package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzStillOK(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Equal(body, []byte("ok\n")) {
		t.Fatalf("healthz body = %q, want %q", body, "ok\n")
	}
}

func TestSPAFallbackServesEmbeddedIndex(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/responses", nil)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SPA fallback status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<title>OpenSight</title>") {
		t.Fatalf("SPA fallback body did not contain embedded index: %q", rec.Body.String())
	}
}

func TestStaticAssetRoutesDoNotFallBackToSPA(t *testing.T) {
	srv := &Server{}

	index := httptest.NewRecorder()
	srv.Routes().ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("index asset status = %d, want 200", index.Code)
	}

	missingAsset := httptest.NewRecorder()
	srv.Routes().ServeHTTP(missingAsset, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if missingAsset.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want 404", missingAsset.Code)
	}
	if strings.Contains(missingAsset.Body.String(), "<title>OpenSight</title>") {
		t.Fatalf("missing asset fell back to index.html: %q", missingAsset.Body.String())
	}
}

func TestRouterMethodNotAllowedUsesProblemJSON(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"title":"method not allowed"`) ||
		!strings.Contains(rec.Body.String(), `"status":405`) {
		t.Fatalf("unexpected problem body %q", rec.Body.String())
	}
}
