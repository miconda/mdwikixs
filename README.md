# MdWikiXS #

Markdown Wiki XS Size

   * [Overview](#overview)
   * [Installation](#installation)
      + [Download Binary Release](#download-binary-release)
   * [Running Wiki](#running-wiki)
      + [Init MdWikiXS](#init-mdwikixs)
      + [Start MdWikiXS](#start-mdwikixs)
      + [Embedded File Server](#embedded-file-server)
   * [Customization](#customization)
   * [Screenshots](#screenshots)
   * [Contributions](#contributions)
   * [License](#license)

## Overview ##

Minimal wiki system using markdown format for pages. It has git storage, keeping the revisions of the pages, allowing to revert at any time.

The default UI is minimal, with a simple navigation bar at the top and button to edit or view old page revisions.
The editing of the markdown content is done in a bare bone text area.  The UI can be customized by editing
the template files (see `templates` folder in the source code tree).

It is written in Go (golang), aiming to be self contained as much as possible, but allow also customization,
with ability to run on local network, even when there is no internet connection. It can be
run without root/admin privileges on Linux, MacOS or Windows.

An important goal is that the outcome of using the wiki can be easily sent to other people that can view them
without running any web server or wiki system. Practically, just archive the `web/pages` folder and send it over.
The markdown files are easy to read with any text editor. If you do not want to include the editing history (the
revisions of the wiki pages) in what is sent out, exclude the `web/pages/.git/` folder when creating the archive.

## Installation ##

To compile `mdwikixs` it is required to install the [Go language](https://golang.org/) and set the required environment variables. The `git` application has to be also installed.

To download the sources, build and install:

```
go get github.com/miconda/mdwikixs
```

The binary is found at:

```
$GOPATH/bin/mdwikixs
```

To download only and build in source code directory, do:

```
go get -d github.com/miconda/mdwikixs
cd $GOPATH/src/github.com/miconda/mdwikixs
go build
```

Then `mdwikixs` has to be run from the source folder, not from `$GOPATH/bin/`.

### Download Binary Release ###

Binary releases for `Linux`, `MacOS` and `Windows` are available at:

  * https://github.com/miconda/mdwikixs/releases

## Running Wiki ##

Create the folder where wiki systems should run:

```
mkdir ~/mdwikixs-site
cd ~/mdwikixs-site
```

### Init MdWikiXS ###

For Version 1.2.0 Or Newer:

```
mdwikixs -init-site-dir
```

For Version Older Than 1.2.0:

```
cp -a $GOPATH/src/github.com/miconda/mdwikixs/templates .
cp -a $GOPATH/src/github.com/miconda/mdwikixs/web .
```

Initialize the git repository for `web/pages` folder and add the `index.md` file:

```
cd ~/mdwikixs-site/web/pages
git init .
git add index.md
git commit index.md -m "added the index.md file"
```

### Start MdWikiXS ###

Run `mdwikixs` inside the wiki main folder.

If `mdwikixs` is deployed in a system `PATH` directory:

```
cd ~/mdwikixs-site/
$GOPATH/bin/mdwikixs
```

If `mdwikixs` is deployed in the `GOPATH` directory:

```
cd ~/mdwikixs-site/
$GOPATH/bin/mdwikixs
```

Visit [http://127.0.0.1:8040/](http://127.0.0.1:8040/).

The `mdwikixs` can listen on different `IP:PORT` as well as serve over `HTTPS`,
see `mdwikixs -h` for command line options.

To add new pages, just edit the index page and add link references, like:

```
# Main Page #

  * [My First Page](page1)
```

Save, then click on `My First Page` link and add content. When save is pressed,
the new page is created in a file named `page1.md`, being added to the git
repository as well. The `mdwikixs` supports also creating subfolders to store
the wiki files, reference them like:

```
[Page In Subfolder](subfolder/page1)
```

The markdown file will be saved in:

```
web/pages/subfolder/page1.md
```

**Important Note**: before clicking on `Save` button be sure the `Change Log`
field (located below the text area with content) is filled, otherwise the changes
are not saved. To cancel editing a page, just click on the page name in the
navigation bar at the top to go back to view the html page.

### Embedded File Server ###

Besides creating markdown files, editing, keeping revision history in git and rendering
the markdown files to html, the `mdwikixs` can serve for download the files stored
in `web/public/` folder. These files can be referenced in wiki pages (markdown
files) like:

```
[File To Download](/public/file-to-download.pdf)
```

## Customization ##

The markdown files are rendered to HTML based on template files located in
`templates` directory. They can be edited to suit better specific needs.

The content of the template files should follow the specifications of the
[Go language templates](https://golang.org/pkg/html/template/).

## Screenshots ##

To keep this repo small in size, to match the goals of `mdwikixs` of XS size,
several screenshots were published in an external repository - see them at:

  * https://github.com/miconda/vresources/tree/master/mdwikixs/screenshots

## Contributions ##

Bug reports must be submitted at:

  * https://github.com/miconda/mdwikixs/issues

Updates to the code have to be sumbitted as pull requests at:

  * https://github.com/miconda/mdwikixs/pulls

## License ##

GPLv3
