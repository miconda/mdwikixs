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
