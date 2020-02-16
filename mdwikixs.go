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
	"strconv"
	"strings"

	"github.com/russross/blackfriday"
)

const mwikixsVersion = "1.0"

// CLIOptions - structure for command line options
type CLIOptions struct {
	httpsrv     string
	httpssrv    string
	httpspubkey string
	httpsprvkey string
	httpdir     string
	version     bool
}

var cliops = CLIOptions{
	httpsrv:     "127.0.0.1:8040",
	httpssrv:    "",
	httpspubkey: "",
	httpsprvkey: "",
	httpdir:     "web",
	version:     false,
}

const (
	dirPages    = "pages"
	dirPublic   = "public"
	gitLogLimit = "5"
)

// MWXSPage - wiki page
type MWXSPage struct {
	Path     string
	File     string
	Content  string
	Template string
	Revision string
	Bytes    []byte
	Dirs     []*MWXSDirectory
	Log      []*MWXSGitLog
	Markdown template.HTML

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
	buf := runGitCmd(exec.Command("git", "log", "--pretty=format:%h %ad %s", "--date=relative", "-n", gitLogLimit, page.File))
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
	changelog := r.FormValue("msg")
	author := r.FormValue("author")
	reset := r.FormValue("revert")
	revision := r.FormValue("revision")

	filePath := fmt.Sprintf("%s/%s%s.md", cliops.httpdir, dirPages, uPath)
	log.Printf("info: serving file: %s\n", filePath)
	page := &MWXSPage{File: uPath[1:] + ".md", Path: filePath}
	page.Revisions = parseBool(r.FormValue("revisions"))

	page.Dirs = listDirectories(uPath)

	if content != "" && changelog != "" {
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
			page.Template = "templates/edit.tpl"
		} else {
			page.toMarkdown()
		}
	}
	renderTemplate(w, page)
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
	t.ParseFiles("templates/header.tpl", "templates/footer.tpl",
		"templates/actions.tpl", "templates/revision.tpl",
		"templates/revisions.tpl", "templates/page.tpl")
	err = t.Execute(w, page)
	if err != nil {
		log.Print("error: could not execute template: ", err)
	}
}

// initialize application components
func init() {
	// command line arguments
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s (v%s):\n", filepath.Base(os.Args[0]), mwikixsVersion)
		flag.PrintDefaults()
		os.Exit(1)
	}

	flag.StringVar(&cliops.httpsrv, "http-srv", cliops.httpsrv, "http server bind address")
	flag.StringVar(&cliops.httpssrv, "https-srv", cliops.httpssrv, "https server bind address")
	flag.StringVar(&cliops.httpspubkey, "https-pubkey", cliops.httpspubkey, "https server public key")
	flag.StringVar(&cliops.httpsprvkey, "https-prvkey", cliops.httpsprvkey, "https server private key")
	flag.StringVar(&cliops.httpdir, "http-dir", cliops.httpdir, "directory to serve over http")
	flag.BoolVar(&cliops.version, "version", cliops.version, "print version")
}

func startHTTPServices() chan error {

	errchan := make(chan error)

	// starting HTTP server
	if len(cliops.httpsrv) > 0 {
		go func() {
			log.Printf("staring HTTP service on: %s ...", cliops.httpsrv)

			if err := http.ListenAndServe(cliops.httpsrv, nil); err != nil {
				errchan <- err
			}

		}()
	}

	// starting HTTPS server
	if len(cliops.httpssrv) > 0 && len(cliops.httpspubkey) > 0 && len(cliops.httpsprvkey) > 0 {
		go func() {
			log.Printf("Staring HTTPS service on: %s ...", cliops.httpssrv)
			if err := http.ListenAndServeTLS(cliops.httpssrv, cliops.httpspubkey, cliops.httpsprvkey, nil); err != nil {
				errchan <- err
			}
		}()
	}

	return errchan
}

func main() {
	flag.Parse()

	// Handlers
	http.HandleFunc("/", wikiHandler)

	// Static resources
	log.Printf("serving files over http from directory: %s\n", cliops.httpdir)

	http.Handle("/public/", http.StripPrefix(strings.TrimRight("/public/", "/"), http.FileServer(http.Dir(cliops.httpdir+"/public"))))
	http.Handle("/assets/", http.StripPrefix(strings.TrimRight("/assets/", "/"), http.FileServer(http.Dir(cliops.httpdir+"/assets"))))

	errchan := startHTTPServices()
	select {
	case err := <-errchan:
		log.Printf("unable to start http services due to (error: %v)", err)
	}
	os.Exit(1)

}
