package main

import (
	"errors"
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

func TestPageNameBeginningWithDash(t *testing.T) {
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

	form := url.Values{
		"content":    []string{"dash-prefixed page"},
		"save":       []string{"true"},
		"msg":        []string{"save dash-prefixed page"},
		"csrf_token": []string{csrfToken},
	}
	request := httptest.NewRequest(http.MethodPost, "/-draft", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	wikiHandler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("save status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if tracked := strings.TrimSpace(runTestGit(t, pagesDir, "ls-files", "--", "-draft.md")); tracked != "-draft.md" {
		t.Fatalf("tracked file = %q, want %q", tracked, "-draft.md")
	}
	if commits := strings.TrimSpace(runTestGit(t, pagesDir, "rev-list", "--count", "HEAD", "--", "-draft.md")); commits != "1" {
		t.Fatalf("page commit count = %q, want 1", commits)
	}

	request = httptest.NewRequest(http.MethodGet, "/-draft", nil)
	response = httptest.NewRecorder()
	wikiHandler(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "dash-prefixed page") {
		t.Fatalf("read response does not contain page content: %s", response.Body.String())
	}
}

func TestRunGitCmdReturnsStderr(t *testing.T) {
	httpDir := t.TempDir()
	pagesDir := filepath.Join(httpDir, dirPages)
	if err := os.MkdirAll(pagesDir, 0755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, pagesDir, "init", "-q")

	previousHTTPDir := cliops.httpdir
	cliops.httpdir = httpDir
	t.Cleanup(func() { cliops.httpdir = previousHTTPDir })

	_, err := runGitCmd(exec.Command("git", "rev-parse", "--verify", "missing-ref"))
	if err == nil {
		t.Fatal("runGitCmd() error = nil, want Git failure")
	}
	if !strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("runGitCmd() error does not include stderr: %v", err)
	}
}

func TestSaveReportsGitFailure(t *testing.T) {
	httpDir := t.TempDir()
	pagesDir := filepath.Join(httpDir, dirPages)
	if err := os.MkdirAll(pagesDir, 0755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, pagesDir, "init", "-q")
	runTestGit(t, pagesDir, "config", "user.name", "MdWikiXS Test")
	runTestGit(t, pagesDir, "config", "user.email", "mdwikixs@example.test")
	if err := os.WriteFile(filepath.Join(pagesDir, ".git", "index.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	previousHTTPDir := cliops.httpdir
	cliops.httpdir = httpDir
	t.Cleanup(func() { cliops.httpdir = previousHTTPDir })

	form := url.Values{
		"content":    []string{"content that cannot be staged"},
		"save":       []string{"true"},
		"msg":        []string{"forced failure"},
		"csrf_token": []string{csrfToken},
	}
	request := httptest.NewRequest(http.MethodPost, "/failed-save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	wikiHandler(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if body := response.Body.String(); !strings.Contains(body, "repository operation failed") {
		t.Errorf("response does not report repository failure: %q", body)
	} else if strings.Contains(body, "index.lock") {
		t.Errorf("response leaks internal Git diagnostic: %q", body)
	}
}

func TestSiteUpdateCloneFailurePreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	if err := writeTestSiteVersion(root, "old"); err != nil {
		t.Fatal(err)
	}

	err := updateSiteDirAt(root, func(string) error {
		return errors.New("download failed")
	}, os.Rename)
	if err == nil {
		t.Fatal("updateSiteDirAt() error = nil, want clone failure")
	}

	assertTestSiteVersion(t, root, "old")
	assertNoUpdateStagingDirectories(t, root)
}

func TestSiteUpdateInvalidDownloadPreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	if err := writeTestSiteVersion(root, "old"); err != nil {
		t.Fatal(err)
	}

	err := updateSiteDirAt(root, func(destination string) error {
		return os.MkdirAll(filepath.Join(destination, "templates"), 0755)
	}, os.Rename)
	if err == nil {
		t.Fatal("updateSiteDirAt() error = nil, want validation failure")
	}

	assertTestSiteVersion(t, root, "old")
	assertNoUpdateStagingDirectories(t, root)
}

func TestSiteUpdateSwapFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	if err := writeTestSiteVersion(root, "old"); err != nil {
		t.Fatal(err)
	}

	failed := false
	rename := func(oldPath, newPath string) error {
		isStagedAssets := strings.Contains(oldPath, filepath.Join("checkout", "web", "assets"))
		if !failed && isStagedAssets && newPath == filepath.Join(root, "web", "assets") {
			failed = true
			return errors.New("forced asset swap failure")
		}
		return os.Rename(oldPath, newPath)
	}
	err := updateSiteDirAt(root, func(destination string) error {
		return writeTestSiteVersion(destination, "new")
	}, rename)
	if err == nil {
		t.Fatal("updateSiteDirAt() error = nil, want swap failure")
	}

	assertTestSiteVersion(t, root, "old")
	assertNoUpdateStagingDirectories(t, root)
}

func TestSiteUpdateReplacesValidatedDirectories(t *testing.T) {
	root := t.TempDir()
	if err := writeTestSiteVersion(root, "old"); err != nil {
		t.Fatal(err)
	}

	err := updateSiteDirAt(root, func(destination string) error {
		return writeTestSiteVersion(destination, "new")
	}, os.Rename)
	if err != nil {
		t.Fatalf("updateSiteDirAt() error = %v", err)
	}

	assertTestSiteVersion(t, root, "new")
	assertNoUpdateStagingDirectories(t, root)
}

func writeTestSiteVersion(root, version string) error {
	for _, directory := range []string{
		filepath.Join(root, "templates"),
		filepath.Join(root, "web", "assets"),
	} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, "version.txt"), []byte(version), 0644); err != nil {
			return err
		}
	}
	return nil
}

func assertTestSiteVersion(t *testing.T, root, want string) {
	t.Helper()
	for _, filename := range []string{
		filepath.Join(root, "templates", "version.txt"),
		filepath.Join(root, "web", "assets", "version.txt"),
	} {
		content, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		if got := string(content); got != want {
			t.Errorf("%s = %q, want %q", filename, got, want)
		}
	}
}

func assertNoUpdateStagingDirectories(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".mdwikixs-update-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("update staging directories were not removed: %v", matches)
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
