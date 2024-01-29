package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/russross/blackfriday"
)

const mwikixsVersion = "1.1.2"

// CLIOptions - structure for command line options
type CLIOptions struct {
	domain      string
	httpsrv     string
	httpssrv    string
	httpsusele  bool
	httpspubkey string
	httpsprvkey string
	httpdir     string
	tpldir      string
	urldir      string
	version     bool
}

var cliops = CLIOptions{
	domain:      "",
	httpsrv:     "127.0.0.1:8040",
	httpssrv:    "",
	httpsusele:  false,
	httpspubkey: "",
	httpsprvkey: "",
	httpdir:     "web",
	tpldir:      "templates",
	urldir:      "",
	version:     false,
}

const (
	dirPages    = "pages"
	dirPublic   = "public"
	dirAssets   = "assets"
	gitLogLimit = "5"
)

// MWXSPage - wiki page
type MWXSPage struct {
	Path       string
	URLBaseDir string
	File       string
	Content    string
	Template   string
	Revision   string
	Bytes      []byte
	Dirs       []*MWXSDirectory
	Log        []*MWXSGitLog
	Markdown   template.HTML

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

func (page *MWXSPage) isHead() bool {
	return len(page.Log) > 0 && page.Revision == page.Log[0].Hash
}

// git command to add page
func (page *MWXSPage) cmdGitAdd() *MWXSPage {
	log.Printf("exec: git add %s\n", page.File)
	runGitCmd(exec.Command("git", "add", page.File))
	return page
}

// git command to commit page with log message
func (page *MWXSPage) cmdGitCommit(msg string, author string) *MWXSPage {
	if author != "" {
		log.Printf("exec: git commit -m '%s' --author...\n", page.File)
		runGitCmd(exec.Command("git", "commit", "-m", msg, fmt.Sprintf("--author='%s <mwikixs@localhost>'", author)))
	} else {
		log.Printf("exec: git commit -m '%s'\n", page.File)
		runGitCmd(exec.Command("git", "commit", "-m", msg))
	}
	return page
}

// git command to fetch page revision
func (page *MWXSPage) cmdGitShow() *MWXSPage {
	log.Printf("exec: git show %s\n", page.Revision+":"+page.File)
	buf := runGitCmd(exec.Command("git", "show", page.Revision+":"+page.File))
	page.Bytes = buf.Bytes()
	return page
}

// git command to fetch page commit log
func (page *MWXSPage) cmdGitLog() *MWXSPage {
	log.Printf("exec: git log ... %s\n", page.File)
	buf := runGitCmd(exec.Command("git", "log", "--pretty=format:%h %ad %s", "--date=relative", "-n",
		gitLogLimit, page.File))
	var err error
	b := bufio.NewReader(buf)
	var bytes []byte
	page.Log = make([]*MWXSGitLog, 0)
	for err == nil {
		bytes, err = b.ReadSlice('\n')
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
	return page
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
func (page *MWXSPage) cmdGitRevert() *MWXSPage {
	log.Printf("info: reverting %s to revision %s", page.File, page.Revision)
	log.Printf("exec: git checkout %s\n", page.Revision+":"+page.File)
	runGitCmd(exec.Command("git", "checkout", page.Revision, "--", page.File))
	return page
}

// Run git command, will currently die on all errors
func runGitCmd(cmd *exec.Cmd) *bytes.Buffer {
	cmd.Dir = fmt.Sprintf("%s/%s/", cliops.httpdir, dirPages)
	var out bytes.Buffer
	cmd.Stdout = &out
	runError := cmd.Run()
	if runError != nil {
		log.Printf("error: (%s) command failed with:\n\"%s\n\"", runError, out.String())
		return bytes.NewBuffer([]byte{})
	}
	return &out
}

// Process page contents
func (page *MWXSPage) toMarkdown() {
	page.Markdown = template.HTML(string(blackfriday.MarkdownCommon(page.Bytes)))
}

func parseBool(value string) bool {
	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return boolValue
}

func wikiHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/favicon.ico" {
		return
	}

	uPath := r.URL.Path
	if uPath == "/" {
		uPath = "/index"
	}

	// Params
	content := r.FormValue("content")
	edit := r.FormValue("edit")
	save := r.FormValue("save")
	changelog := r.FormValue("msg")
	author := r.FormValue("author")
	reset := r.FormValue("revert")
	revision := r.FormValue("revision")

	filePath := fmt.Sprintf("%s/%s%s.md", cliops.httpdir, dirPages, uPath)
	log.Printf("info: serving file: %s\n", filePath)
	page := &MWXSPage{File: uPath[1:] + ".md", Path: filePath}
	page.Revisions = parseBool(r.FormValue("revisions"))

	page.Dirs = listDirectories(uPath)
	page.URLBaseDir = strings.TrimRight(cliops.urldir, "/")

	if content != "" && save == "true" {
		if changelog == "" {
			now := time.Now()
			changelog = "by " + GetPeerIP(r) + " at " + now.Format(time.RFC3339)
		}
		bytes := []byte(content)
		err := writeFile(bytes, filePath)
		if err != nil {
			log.Printf("error: cannot write to file %s: %v\n", filePath, err)
		} else {
			// Wrote file, commit
			page.Bytes = bytes
			page.cmdGitAdd().cmdGitCommit(changelog, author).cmdGitLog()
			page.toMarkdown()
		}
	} else if reset != "" {
		// Reset to revision
		page.Revision = reset
		page.cmdGitRevert().cmdGitCommit("reverted to: "+page.Revision, author)
		page.Revision = ""
		page.cmdGitShow().cmdGitLog().toMarkdown()
	} else {
		// Show specific revision
		page.Revision = revision
		page.cmdGitShow().cmdGitLog()
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
		return ioutil.WriteFile(entry, bytes, 0644)
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

	if cliops.httpsusele && len(cliops.domain) == 0 {
		log.Printf("use-letsencrypt requires domain parameter\n")
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
