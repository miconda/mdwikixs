package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeMutation(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		token      string
		wantOK     bool
		wantStatus int
	}{
		{
			name:       "rejects GET",
			method:     http.MethodGet,
			token:      csrfToken,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "rejects missing token",
			method:     http.MethodPost,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "rejects wrong token",
			method:     http.MethodPost,
			token:      "wrong-token",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "accepts valid POST",
			method:     http.MethodPost,
			token:      csrfToken,
			wantOK:     true,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{"csrf_token": []string{tt.token}}
			req := httptest.NewRequest(tt.method, "/page", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()

			if got := authorizeMutation(response, req); got != tt.wantOK {
				t.Fatalf("authorizeMutation() = %v, want %v", got, tt.wantOK)
			}
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.method != http.MethodPost && response.Header().Get("Allow") != http.MethodPost {
				t.Fatalf("Allow header = %q, want %q", response.Header().Get("Allow"), http.MethodPost)
			}
		})
	}
}

func TestWikiHandlerRejectsUnauthorizedMutations(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{
			name:       "save over GET",
			method:     http.MethodGet,
			target:     "/page?save=true&content=forged",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "save without token",
			method:     http.MethodPost,
			target:     "/page?save=true&content=forged",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "revert over GET",
			method:     http.MethodGet,
			target:     "/page?revert=deadbeef",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "revert without token",
			method:     http.MethodPost,
			target:     "/page?revert=deadbeef",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.target, nil)
			response := httptest.NewRecorder()

			wikiHandler(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestMarkdownRenderingRejectsXSS(t *testing.T) {
	tests := []struct {
		name     string
		markdown string
		forbid   []string
	}{
		{
			name:     "script block",
			markdown: "<script>alert('xss')</script>",
			forbid:   []string{"<script", "alert("},
		},
		{
			name:     "event handler",
			markdown: `<img src="x" onerror="alert('xss')">`,
			forbid:   []string{"<img", "onerror", "alert("},
		},
		{
			name:     "unsafe link",
			markdown: "[click](javascript:alert('xss'))",
			forbid:   []string{"javascript:", "href="},
		},
		{
			name:     "unsafe image",
			markdown: "![image](data:text/html;base64,PHNjcmlwdD4=)",
			forbid:   []string{"data:", "src="},
		},
		{
			name:     "unsafe custom header ID",
			markdown: `# Heading {#"><img src=x onerror=alert('xss')>}`,
			forbid:   []string{"<img", "onerror", "alert(", `id="`},
		},
		{
			name:     "obfuscated unsafe link",
			markdown: "[click](JaVaScRiPt:alert('xss'))",
			forbid:   []string{"javascript:", "href="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := &MWXSPage{Bytes: []byte(tt.markdown)}
			page.toMarkdown()
			rendered := strings.ToLower(string(page.Markdown))

			for _, forbidden := range tt.forbid {
				if strings.Contains(rendered, forbidden) {
					t.Fatalf("rendered Markdown contains %q: %s", forbidden, rendered)
				}
			}
		})
	}
}

func TestMarkdownRenderingPreservesSafeFormatting(t *testing.T) {
	page := &MWXSPage{Bytes: []byte("# Heading\n\n**bold**, [safe](https://example.com), and [wiki page](page1)")}
	page.toMarkdown()
	rendered := string(page.Markdown)

	for _, expected := range []string{
		"<h1>Heading</h1>",
		"<strong>bold</strong>",
		`href="https://example.com"`,
		`href="page1"`,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered Markdown does not contain %q: %s", expected, rendered)
		}
	}
}
