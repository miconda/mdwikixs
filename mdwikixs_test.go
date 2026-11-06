package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestConcurrentSavesCreateSeparateCommits(t *testing.T) {
	httpDir := t.TempDir()
	pagesDir := filepath.Join(httpDir, dirPages)
	if err := os.MkdirAll(pagesDir, 0755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, pagesDir, "init", "-q")
	runTestGit(t, pagesDir, "config", "user.name", "MdWikiXS Test")
	runTestGit(t, pagesDir, "config", "user.email", "mdwikixs@example.test")

	previousHTTPDir := cliops.httpdir
	cliops.httpdir = httpDir
	t.Cleanup(func() { cliops.httpdir = previousHTTPDir })

	const saves = 8
	start := make(chan struct{})
	results := make(chan string, saves)
	var workers sync.WaitGroup
	workers.Add(saves)

	for i := 0; i < saves; i++ {
		go func(index int) {
			defer workers.Done()
			<-start

			form := url.Values{
				"content":    []string{fmt.Sprintf("page %d", index)},
				"save":       []string{"true"},
				"msg":        []string{fmt.Sprintf("save page %d", index)},
				"csrf_token": []string{csrfToken},
			}
			request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/page-%d", index), strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()

			wikiHandler(response, request)
			if response.Code != http.StatusOK {
				results <- fmt.Sprintf("save %d returned status %d", index, response.Code)
			}
		}(i)
	}

	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		t.Error(result)
	}

	commitCount, err := strconv.Atoi(strings.TrimSpace(runTestGit(t, pagesDir, "rev-list", "--count", "HEAD")))
	if err != nil {
		t.Fatalf("invalid commit count: %v", err)
	}
	if commitCount != saves {
		t.Errorf("commit count = %d, want %d", commitCount, saves)
	}
	if status := runTestGit(t, pagesDir, "status", "--porcelain"); status != "" {
		t.Errorf("repository has uncommitted changes:\n%s", status)
	}
}

func runTestGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
