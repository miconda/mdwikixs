package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/russross/blackfriday"
)

const mwikixsVersion = "1.2.2"

// CLIOptions - structure for command line options
type CLIOptions struct {
	domain        string
	httpsrv       string
	httpssrv      string
	httpsusele    bool
	httpspubkey   string
	httpsprvkey   string
	httpdir       string
	tpldir        string
	urldir        string
	initsitedir   bool
	updatesitedir bool
	version       bool
}

var cliops = CLIOptions{
	domain:        "",
	httpsrv:       "127.0.0.1:8040",
	httpssrv:      "",
	httpsusele:    false,
	httpspubkey:   "",
	httpsprvkey:   "",
	httpdir:       "web",
	tpldir:        "templates",
	urldir:        "",
	initsitedir:   false,
	updatesitedir: false,
	version:       false,
}

const (
	dirPages    = "pages"
	dirPublic   = "public"
	dirAssets   = "assets"
	gitLogLimit = "5"

	markdownExtensions = blackfriday.EXTENSION_NO_INTRA_EMPHASIS |
		blackfriday.EXTENSION_TABLES |
		blackfriday.EXTENSION_FENCED_CODE |
		blackfriday.EXTENSION_AUTOLINK |
		blackfriday.EXTENSION_STRIKETHROUGH |
		blackfriday.EXTENSION_SPACE_HEADERS |
		blackfriday.EXTENSION_HEADER_IDS |
		blackfriday.EXTENSION_BACKSLASH_LINE_BREAK |
		blackfriday.EXTENSION_DEFINITION_LISTS

	markdownHTMLFlags = blackfriday.HTML_USE_XHTML |
		blackfriday.HTML_USE_SMARTYPANTS |
		blackfriday.HTML_SMARTYPANTS_FRACTIONS |
		blackfriday.HTML_SMARTYPANTS_DASHES |
		blackfriday.HTML_SMARTYPANTS_LATEX_DASHES |
		blackfriday.HTML_SKIP_HTML
)

// MWXSPage - wiki page
type MWXSPage struct {
	Path       string
	URLBaseDir string
	File       string
	Content    string
	Template   string
	Revision   string
	MWXVersion string
	Bytes      []byte
	Dirs       []*MWXSDirectory
	Log        []*MWXSGitLog
	Markdown   template.HTML
	CSRFToken  string

	Revisions bool // Show revisions
}

// MWXSDirectory - directory info
type MWXSDirectory struct {
	Path   string
	Name   string
	Active bool
}

// MWXSGitLog - git log info
type MWXSGitLog struct {
	Hash    string
	Message string
	Time    string
	Link    bool
}

type MWXHandler struct {
	address string
}

type safeHTMLRenderer struct {
	blackfriday.Renderer
}

func safeURL(destination []byte, allowedSchemes ...string) bool {
	raw := string(destination)
	if strings.TrimSpace(raw) != raw {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		return true
	}
	for _, scheme := range allowedSchemes {
		if strings.EqualFold(parsed.Scheme, scheme) {
			return true
		}
	}
	return false
}

func (renderer safeHTMLRenderer) AutoLink(out *bytes.Buffer, link []byte, kind int) {
	if kind == blackfriday.LINK_TYPE_EMAIL || safeURL(link, "http", "https", "ftp", "mailto") {
		renderer.Renderer.AutoLink(out, link, kind)
	}
}

func (renderer safeHTMLRenderer) Link(out *bytes.Buffer, link []byte, title []byte, content []byte) {
	if safeURL(link, "http", "https", "ftp", "mailto") {
		renderer.Renderer.Link(out, link, title, content)
		return
	}
	// Keep the visible label when an unsafe destination is rejected.
	out.Write(content)
}

func (renderer safeHTMLRenderer) Image(out *bytes.Buffer, link []byte, title []byte, alt []byte) {
	if safeURL(link, "http", "https") {
		renderer.Renderer.Image(out, link, title, alt)
	}
}

func (renderer safeHTMLRenderer) Header(out *bytes.Buffer, text func() bool, level int, id string) {
	for _, char := range id {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_' || char == ':' || char == '.') {
			id = ""
			break
		}
	}
	renderer.Renderer.Header(out, text, level, id)
}

func (page *MWXSPage) isHead() bool {
	return len(page.Log) > 0 && page.Revision == page.Log[0].Hash
}

// git command to add page
func (page *MWXSPage) cmdGitAdd() error {
	log.Printf("exec: git add %s\n", page.File)
	_, err := runGitCmd(exec.Command("git", "add", "--", page.File))
	return err
}

// git command to commit page with log message
func (page *MWXSPage) cmdGitCommit(msg string, author string) error {
	var command *exec.Cmd
	if author != "" {
		log.Printf("exec: git commit -m '%s' --author...\n", page.File)
		command = exec.Command("git", "commit", "-m", msg, fmt.Sprintf("--author='%s <mwikixs@localhost>'", author))
	} else {
		log.Printf("exec: git commit -m '%s'\n", page.File)
		command = exec.Command("git", "commit", "-m", msg)
	}
	_, err := runGitCmd(command)
	return err
}

// git command to fetch page revision
func (page *MWXSPage) cmdGitShow() error {
	log.Printf("exec: git show %s\n", page.Revision+":"+page.File)
	buf, err := runGitCmd(exec.Command("git", "show", page.Revision+":"+page.File))
	if err != nil {
		return err
	}
	page.Bytes = buf.Bytes()
	return nil
}

// git command to fetch page commit log
func (page *MWXSPage) cmdGitLog() error {
	log.Printf("exec: git log ... %s\n", page.File)
	buf, err := runGitCmd(exec.Command("git", "log", "--pretty=format:%h %ad %s", "--date=relative", "-n",
		gitLogLimit, "--", page.File))
	if err != nil {
		return err
	}
	var readErr error
	b := bufio.NewReader(buf)
	var bytes []byte
	page.Log = make([]*MWXSGitLog, 0)
	for readErr == nil {
		bytes, readErr = b.ReadSlice('\n')
		logLine := parseLog(bytes)
		if logLine == nil {
			continue
		} else if logLine.Hash != page.Revision {
			logLine.Link = true
		}
		page.Log = append(page.Log, logLine)
	}
	if page.Revision == "" && len(page.Log) > 0 {
		page.Revision = page.Log[0].Hash
		page.Log[0].Link = false
	}
	return nil
}

func GetPeerIP(r *http.Request) string {
	PeerIPAddr := r.Header.Get("X-Real-Ip")
	if PeerIPAddr == "" {
		PeerIPAddr = r.Header.Get("X-Forwarded-For")
	}
	if PeerIPAddr == "" {
		PeerIPAddr = r.RemoteAddr
	}
	return PeerIPAddr
}

func parseLog(bytes []byte) *MWXSGitLog {
	line := string(bytes)
	re := regexp.MustCompile(`(.{0,7}) (\d+ \w+ ago) (.*)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) == 4 {
		return &MWXSGitLog{Hash: matches[1], Time: matches[2], Message: matches[3]}
	}
	return nil
}

func listDirectories(path string) []*MWXSDirectory {
	s := make([]*MWXSDirectory, 0)
	dirPath := ""
	for i, dir := range strings.Split(path, "/") {
		if i == 0 {
			dirPath += dir
		} else {
			dirPath += "/" + dir
		}
		s = append(s, &MWXSDirectory{Path: dirPath, Name: dir})
	}
	if len(s) > 0 {
		s[len(s)-1].Active = true
	}
	return s
}

// Soft reset to specific revision
func (page *MWXSPage) cmdGitRevert() error {
	log.Printf("info: reverting %s to revision %s", page.File, page.Revision)
	log.Printf("exec: git checkout %s\n", page.Revision+":"+page.File)
	_, err := runGitCmd(exec.Command("git", "checkout", page.Revision, "--", page.File))
	return err
}

// Run a Git command and preserve its diagnostics for the caller.
func runGitCmd(cmd *exec.Cmd) (*bytes.Buffer, error) {
	cmd.Dir = fmt.Sprintf("%s/%s/", cliops.httpdir, dirPages)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if diagnostic == "" {
			diagnostic = strings.TrimSpace(stdout.String())
		}
		if diagnostic != "" {
			return &stdout, fmt.Errorf("%s failed: %w: %s", strings.Join(cmd.Args, " "), err, diagnostic)
		}
		return &stdout, fmt.Errorf("%s failed: %w", strings.Join(cmd.Args, " "), err)
	}
	return &stdout, nil
}

// Process page contents
func (page *MWXSPage) toMarkdown() {
	renderer := safeHTMLRenderer{Renderer: blackfriday.HtmlRenderer(markdownHTMLFlags, "", "")}
	rendered := blackfriday.MarkdownOptions(page.Bytes, renderer, blackfriday.Options{
		Extensions: markdownExtensions,
	})
	// The renderer omits raw HTML and rejects unsafe link schemes, so its
	// generated markup is safe to pass through html/template without escaping.
	page.Markdown = template.HTML(rendered)
}

func parseBool(value string) bool {
	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return boolValue
}

func newCSRFToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Fatalf("error: cannot generate CSRF token: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

var csrfToken = newCSRFToken()

// gitRepoMu protects the shared worktree and Git index. A write lock spans an
// entire filesystem/Git transaction so one request cannot commit another
// request's staged changes. Readers wait until that transaction is complete.
var gitRepoMu sync.RWMutex

func authorizeMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}

	requestToken := r.FormValue("csrf_token")
	if subtle.ConstantTimeCompare([]byte(requestToken), []byte(csrfToken)) != 1 {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return false
	}

	return true
}

func reportRepositoryError(w http.ResponseWriter, err error) {
	log.Printf("error: repository operation failed: %v", err)
	http.Error(w, "repository operation failed", http.StatusInternalServerError)
}

func wikiHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		return
	}

	uPath := r.URL.Path
	if uPath == "/" {
		uPath = "/index"
	}
	if strings.Index(uPath, "..") != -1 {
		log.Printf("error: invalid URL path %s\n", uPath)
		return
	}

	// Params
	content := r.FormValue("content")
	edit := r.FormValue("edit")
	save := r.FormValue("save")
	changelog := r.FormValue("msg")
	author := r.FormValue("author")
	reset := r.FormValue("revert")
	revision := r.FormValue("revision")
	if (save == "true" || reset != "") && !authorizeMutation(w, r) {
		return
	}

	filePath := fmt.Sprintf("%s/%s%s.md", cliops.httpdir, dirPages, uPath)
	log.Printf("info: serving file: %s\n", filePath)
	page := &MWXSPage{File: uPath[1:] + ".md", Path: filePath}
	page.Revisions = parseBool(r.FormValue("revisions"))

	page.MWXVersion = mwikixsVersion
	page.CSRFToken = csrfToken
	page.Dirs = listDirectories(uPath)
	page.URLBaseDir = strings.TrimRight(cliops.urldir, "/")

	if content != "" && save == "true" {
		if changelog == "" {
			now := time.Now()
			changelog = "by " + GetPeerIP(r) + " at " + now.Format(time.RFC3339)
		}
		err := func() error {
			gitRepoMu.Lock()
			defer gitRepoMu.Unlock()

			bytes := []byte(content)
			if err := writeFile(bytes, filePath); err != nil {
				return fmt.Errorf("write page %s: %w", page.File, err)
			}
			// Wrote file, commit
			page.Bytes = bytes
			if err := page.cmdGitAdd(); err != nil {
				return err
			}
			if err := page.cmdGitCommit(changelog, author); err != nil {
				return err
			}
			if err := page.cmdGitLog(); err != nil {
				return err
			}
			page.toMarkdown()
			return nil
		}()
		if err != nil {
			reportRepositoryError(w, err)
			return
		}
	} else if reset != "" {
		err := func() error {
			gitRepoMu.Lock()
			defer gitRepoMu.Unlock()

			// Reset to revision
			page.Revision = reset
			if err := page.cmdGitRevert(); err != nil {
				return err
			}
			if err := page.cmdGitCommit("reverted to: "+page.Revision, author); err != nil {
				return err
			}
			page.Revision = ""
			if err := page.cmdGitShow(); err != nil {
				return err
			}
			if err := page.cmdGitLog(); err != nil {
				return err
			}
			page.toMarkdown()
			return nil
		}()
		if err != nil {
			reportRepositoryError(w, err)
			return
		}
	} else {
		err := func() error {
			gitRepoMu.RLock()
			defer gitRepoMu.RUnlock()

			// Show specific revision
			page.Revision = revision
			if revision == "" {
				if _, err := os.Stat(filePath); errors.Is(err, os.ErrNotExist) {
					return nil
				} else if err != nil {
					return fmt.Errorf("stat page %s: %w", page.File, err)
				}
			}
			if err := page.cmdGitShow(); err != nil {
				return err
			}
			if err := page.cmdGitLog(); err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			reportRepositoryError(w, err)
			return
		}
		if edit == "true" || len(page.Bytes) == 0 {
			page.Content = string(page.Bytes)
			page.Template = cliops.tpldir + "/edit.tpl"
		} else {
			page.toMarkdown()
		}
	}
	renderTemplate(w, page)
}

func (mhandler *MWXHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	wikiHandler(w, r)
}

func writeFile(bytes []byte, entry string) error {
	err := os.MkdirAll(path.Dir(entry), 0777)
	if err == nil {
		return os.WriteFile(entry, bytes, 0644)
	}

	return err
}

func renderTemplate(w http.ResponseWriter, page *MWXSPage) {

	t := template.New("wiki")
	var err error

	if page.Template != "" {
		t, err = template.ParseFiles(page.Template)
		if err != nil {
			log.Print("error: could not parse template: ", err)
		}
	} else if page.Markdown != "" {
		tpl := "{{ template \"header\" . }}"
		if page.isHead() {
			tpl += "{{ template \"actions\" . }}"
		} else if page.Revision != "" {
			tpl += "{{ template \"revision\" . }}"
		}
		// Add page
		tpl += "{{ template \"page\" . }}"
		// Show revisions
		if page.Revisions {
			tpl += "{{ template \"revisions\" . }}"
		}

		// Footer
		tpl += "{{ template \"footer\" . }}"
		t.Parse(tpl)
	}

	// Include the rest
	t.ParseFiles(cliops.tpldir+"/header.tpl", cliops.tpldir+"/footer.tpl",
		cliops.tpldir+"/actions.tpl", cliops.tpldir+"/revision.tpl",
		cliops.tpldir+"/revisions.tpl", cliops.tpldir+"/page.tpl")
	err = t.Execute(w, page)
	if err != nil {
		log.Print("error: could not execute template: ", err)
	}
}

func initSiteDir() int {
	shellCmds := "git clone --depth 1 https://github.com/miconda/mdwikixs _tmp_mdwikixs_git;" +
		"mv ./_tmp_mdwikixs_git/web .;" +
		"mv ./_tmp_mdwikixs_git/templates .;" +
		"rm -rf ./_tmp_mdwikixs_git;" +
		"cd ./web/pages;" +
		"git init .;" +
		"git add .;" +
		"git commit . -m 'initializing';" +
		"cd ../../;"

	log.Printf("starting site directory initialization...\n")
	cmd := exec.Command("sh", "-c", shellCmds)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Print("error: ", err)
		return -1
	}

	return 0
}

func updateSiteDir() int {
	log.Printf("starting site directory update...\n")
	if err := updateSiteDirAt(".", cloneSiteRepository, os.Rename); err != nil {
		log.Printf("error: site directory update failed: %v", err)
		return -1
	}

	return 0
}

type siteCloneFunc func(destination string) error
type renameFunc func(oldPath, newPath string) error

func cloneSiteRepository(destination string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", "https://github.com/miconda/mdwikixs", destination)
	cmd.Stdout = os.Stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if diagnostic := strings.TrimSpace(stderr.String()); diagnostic != "" {
			return fmt.Errorf("git clone failed: %w: %s", err, diagnostic)
		}
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

func updateSiteDirAt(root string, clone siteCloneFunc, rename renameFunc) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve site directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(root, ".mdwikixs-update-")
	if err != nil {
		return fmt.Errorf("create update staging directory: %w", err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			if err := os.RemoveAll(stagingDir); err != nil {
				log.Printf("error: cannot remove update staging directory %s: %v", stagingDir, err)
			}
		}
	}()

	checkoutDir := filepath.Join(stagingDir, "checkout")
	if err := clone(checkoutDir); err != nil {
		return err
	}

	replacements := []siteReplacement{
		{
			source: filepath.Join(checkoutDir, "templates"),
			target: filepath.Join(root, "templates"),
			backup: filepath.Join(stagingDir, "backup-templates"),
		},
		{
			source: filepath.Join(checkoutDir, "web", "assets"),
			target: filepath.Join(root, "web", "assets"),
			backup: filepath.Join(stagingDir, "backup-assets"),
		},
	}

	for _, replacement := range replacements {
		if err := requireDirectory(replacement.source); err != nil {
			return fmt.Errorf("invalid downloaded site: %w", err)
		}
		if err := requireDirectory(replacement.target); err != nil {
			return fmt.Errorf("invalid existing site: %w", err)
		}
	}

	canCleanup, err := replaceSiteDirectories(replacements, rename)
	if !canCleanup {
		cleanupStaging = false
		return fmt.Errorf("%w; recovery files retained in %s", err, stagingDir)
	}
	return err
}

type siteReplacement struct {
	source    string
	target    string
	backup    string
	backedUp  bool
	installed bool
}

func requireDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("access directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", directory)
	}
	return nil
}

func replaceSiteDirectories(replacements []siteReplacement, rename renameFunc) (bool, error) {
	rollback := func(last int) error {
		var rollbackErrors []error
		for i := last; i >= 0; i-- {
			replacement := &replacements[i]
			if replacement.installed {
				if err := os.RemoveAll(replacement.target); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("remove replacement %s: %w", replacement.target, err))
					continue
				}
				replacement.installed = false
			}
			if replacement.backedUp {
				if err := rename(replacement.backup, replacement.target); err != nil {
					rollbackErrors = append(rollbackErrors, fmt.Errorf("restore backup %s: %w", replacement.target, err))
					continue
				}
				replacement.backedUp = false
			}
		}
		return errors.Join(rollbackErrors...)
	}

	for i := range replacements {
		replacement := &replacements[i]
		if err := rename(replacement.target, replacement.backup); err != nil {
			operationErr := fmt.Errorf("back up %s: %w", replacement.target, err)
			if rollbackErr := rollback(i - 1); rollbackErr != nil {
				return false, errors.Join(operationErr, fmt.Errorf("rollback failed: %w", rollbackErr))
			}
			return true, operationErr
		}
		replacement.backedUp = true
		if err := rename(replacement.source, replacement.target); err != nil {
			operationErr := fmt.Errorf("install %s: %w", replacement.target, err)
			if rollbackErr := rollback(i); rollbackErr != nil {
				return false, errors.Join(operationErr, fmt.Errorf("rollback failed: %w", rollbackErr))
			}
			return true, operationErr
		}
		replacement.installed = true
	}

	return true, nil
}

func printCLIOptions() {
	type CLIOptionDef struct {
		Ops      []string
		Usage    string
		DefValue string
		VType    string
	}
	var items []CLIOptionDef
	flag.VisitAll(func(f *flag.Flag) {
		var found bool = false
		for idx, it := range items {
			if it.Usage == f.Usage {
				found = true
				it.Ops = append(it.Ops, f.Name)
				items[idx] = it
			}
		}
		if !found {
			items = append(items, CLIOptionDef{
				Ops:      []string{f.Name},
				Usage:    f.Usage,
				DefValue: f.DefValue,
				VType:    fmt.Sprintf("%T", f.Value),
			})
		}
	})
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Ops[0]) < strings.ToLower(items[j].Ops[0]) })
	for _, val := range items {
		vtype := val.VType[6 : len(val.VType)-5]
		if vtype[len(vtype)-2:] == "64" {
			vtype = vtype[:len(vtype)-2]
		}
		for _, opt := range val.Ops {
			if vtype == "bool" {
				fmt.Printf("  -%s\n", opt)
			} else {
				fmt.Printf("  -%s %s\n", opt, vtype)
			}
		}
		if vtype != "bool" && len(val.DefValue) > 0 {
			fmt.Printf("      %s [default: %s]\n", val.Usage, val.DefValue)
		} else {
			fmt.Printf("      %s\n", val.Usage)
		}
	}
}

// initialize application components
func init() {
	// command line arguments
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s (v%s):\n", filepath.Base(os.Args[0]), mwikixsVersion)
		printCLIOptions()
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}

	flag.StringVar(&cliops.domain, "domain", cliops.domain, "http service domain")
	flag.StringVar(&cliops.httpsrv, "http-srv", cliops.httpsrv, "http server bind address")
	flag.StringVar(&cliops.httpssrv, "https-srv", cliops.httpssrv, "https server bind address")
	flag.StringVar(&cliops.httpspubkey, "https-pubkey", cliops.httpspubkey, "https server public key")
	flag.StringVar(&cliops.httpsprvkey, "https-prvkey", cliops.httpsprvkey, "https server private key")
	flag.BoolVar(&cliops.httpsusele, "use-letsencrypt", cliops.httpsusele,
		"use local letsencrypt certificates (requires domain)")
	flag.StringVar(&cliops.httpdir, "http-dir", cliops.httpdir, "directory to serve over http")
	flag.StringVar(&cliops.tpldir, "tpl-dir", cliops.tpldir, "directory with template files")
	flag.StringVar(&cliops.urldir, "url-dir", cliops.urldir, "base directory for URL")
	flag.BoolVar(&cliops.initsitedir, "init-site-dir", cliops.initsitedir, "initialize site directory")
	flag.BoolVar(&cliops.updatesitedir, "update-site-dir", cliops.updatesitedir, "update site directory")
	flag.BoolVar(&cliops.version, "version", cliops.version, "print version")
}

func startHTTPServices() chan error {

	errchan := make(chan error)

	// starting HTTP server
	if len(cliops.httpsrv) > 0 {
		go func() {
			if len(cliops.urldir) > 0 {
				log.Printf("staring HTTP service on: http://%s%s ...", cliops.httpsrv, cliops.urldir)
			} else {
				log.Printf("staring HTTP service on: http://%s ...", cliops.httpsrv)
			}

			if err := http.ListenAndServe(cliops.httpsrv, nil); err != nil {
				errchan <- err
			}

		}()
	}

	// starting HTTPS server
	if len(cliops.httpssrv) > 0 && len(cliops.httpspubkey) > 0 && len(cliops.httpsprvkey) > 0 {
		go func() {
			if len(cliops.urldir) > 0 {
				log.Printf("Staring HTTPS service on: https://%s%s ...", cliops.httpssrv, cliops.urldir)
			} else {
				log.Printf("Staring HTTPS service on: https://%s ...", cliops.httpssrv)
			}
			if len(cliops.domain) > 0 {
				dtoken := strings.Split(strings.TrimSpace(cliops.httpssrv), ":")
				if len(cliops.urldir) > 0 {
					log.Printf("HTTPS with domain: https://%s:%s%s ...", cliops.domain, dtoken[1], cliops.urldir)
				} else {
					log.Printf("HTTPS with domain: https://%s:%s ...", cliops.domain, dtoken[1])
				}
			}
			if err := http.ListenAndServeTLS(cliops.httpssrv, cliops.httpspubkey, cliops.httpsprvkey, nil); err != nil {
				errchan <- err
			}
		}()
	}

	return errchan
}

func main() {
	flag.Parse()

	if cliops.version {
		fmt.Println("MdWikiXS", mwikixsVersion)
		return
	}
	if cliops.httpsusele && len(cliops.domain) == 0 {
		log.Printf("use-letsencrypt requires domain parameter\n")
		os.Exit(1)
	}
	if _, err := exec.LookPath("git"); err != nil {
		log.Printf("git command cannot be found\n")
		os.Exit(1)
	}
	if cliops.initsitedir {
		if initSiteDir() < 0 {
			log.Printf("site directory initialization failed\n")
			os.Exit(1)
		} else {
			log.Printf("site directory initialized\n")
			os.Exit(0)
		}
	}
	if cliops.updatesitedir {
		if updateSiteDir() < 0 {
			log.Printf("site directory update failed\n")
			os.Exit(1)
		} else {
			log.Printf("site directory updated\n")
			os.Exit(0)
		}
	}
	if _, err := os.Stat(cliops.tpldir); os.IsNotExist(err) {
		log.Printf(cliops.tpldir + " folder cannot be found\n")
		os.Exit(1)
	}
	if _, err := os.Stat(cliops.httpdir); os.IsNotExist(err) {
		log.Printf(cliops.httpdir + " folder cannot be found\n")
		os.Exit(1)
	}
	if _, err := os.Stat(cliops.httpdir + "/" + dirAssets); os.IsNotExist(err) {
		log.Printf(cliops.httpdir + "/" + dirAssets + " folder cannot be found\n")
		os.Exit(1)
	}
	if _, err := os.Stat(cliops.httpdir + "/" + dirPages); os.IsNotExist(err) {
		log.Printf(cliops.httpdir + "/" + dirPages + " folder cannot be found\n")
		os.Exit(1)
	}
	if _, err := os.Stat(cliops.httpdir + "/" + dirPublic); os.IsNotExist(err) {
		log.Printf(cliops.httpdir + "/" + dirPublic + " folder cannot be found\n")
		os.Exit(1)
	}
	if cliops.httpsusele && len(cliops.httpssrv) > 0 && len(cliops.domain) > 0 {
		cliops.httpspubkey = "/etc/letsencrypt/live/" + cliops.domain + "/fullchain.pem"
		cliops.httpsprvkey = "/etc/letsencrypt/live/" + cliops.domain + "/privkey.pem"
	}

	if len(cliops.urldir) == 0 {
		cliops.urldir = "/"
	} else {
		if !strings.HasPrefix(cliops.urldir, "/") {
			cliops.urldir = "/" + cliops.urldir
		}
		if !strings.HasSuffix(cliops.urldir, "/") {
			cliops.urldir = cliops.urldir + "/"
		}
	}

	// Handlers
	if cliops.urldir == "/" {
		http.HandleFunc(cliops.urldir, wikiHandler)
	} else {
		http.Handle(cliops.urldir, http.StripPrefix(strings.TrimRight(cliops.urldir, "/"), new(MWXHandler)))
	}

	// Static resources
	log.Printf("serving files over http from directory: %s\n", cliops.httpdir)

	http.Handle(cliops.urldir+dirPublic+"/", http.StripPrefix(strings.TrimRight(cliops.urldir+dirPublic+"/", "/"),
		http.FileServer(http.Dir(cliops.httpdir+"/"+dirPublic))))
	http.Handle(cliops.urldir+dirAssets+"/", http.StripPrefix(strings.TrimRight(cliops.urldir+dirAssets+"/", "/"),
		http.FileServer(http.Dir(cliops.httpdir+"/"+dirAssets))))

	errchan := startHTTPServices()
	select {
	case err := <-errchan:
		log.Printf("unable to start http services due to (error: %v)", err)
	}
	os.Exit(1)
}
