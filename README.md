# MdWikiXS #

Markdown Wiki XS Size

## Overview ##

Minimal wiki system using markdown format for pages. It has git storage, keeping the revisions of the pages, allowing to revert at any time.

It is written in Go (golang), aiming to be self contained and run on local network, even when there is no internet connection.

## Installation ##

To compile `mdwikixs` it is required to install the [Go language](https://golang.org/) and set the required environment variables.

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

## Running Wiki ##

Create the folder where wiki systems should run:

```
mkdir ~/mdwikixs-site
cd ~/mdwikixs-site
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

Run `mdwikixs` inside the wiki main folder:

```
cd ~/mdwikixs-site/
$GOPATH/bin/mdwikixs
```

Visit [http://127.0.0.8040/](http://127.0.0.8040/).

The `mdwikixs` can listen on different `IP:PORT` as well as serve over `HTTPS`,
see `mdwikixs -h` for command line options.

To add new pages, just edit the index page and add link references, like:

```
# Main Page #

  * [My First Page](page1)
```

Save, then click on `My First Page` link and add content. When save is pressed,
the new page is created in a file named `page1.md`, being added to the git
repository as well.

**Important Note**: before clicking on `Save` button be sure the `Change Log`
filed (located below the text area with content) is filled, otherwise the changes
are not saved.

## Customization ##

The markdown files are rendered to HTML based on template files located in
`templates` directory. They can be edited to suit better specific needs.

The content of the template files should follow the specifications of the
[Go language templates](https://golang.org/pkg/html/template/).

## Contributions ##

Bug reports must be submitted at:

  * https://github.com/miconda/mdwikixs/issues

Updates to the code have to be sumbitted as pull requests at:

  * https://github.com/miconda/mdwikixs/pulls

## License ##

GPLv3