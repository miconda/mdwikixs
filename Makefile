# Makefile to build the app and deploy

TOOLNAME ?= mdwikixs
WEBSITEDIR ?= /tmp/${TOOLNAME}/site

GO ?= go

OS := $(shell uname -s | sed -e s/SunOS/solaris/ -e s/CYGWIN.*/cygwin/ \
		 | tr "[A-Z]" "[a-z]" | tr "/" "_")

.PHONY: all
all: tool

.PHONY: tool
tool:
	${GO} build -o ${TOOLNAME} .

.PHONY: clean
clean:
	rm -f ${TOOLNAME}

.PHONY: deploy
deploy: tool
	mkdir -p ${WEBSITEDIR}
	cp -a templates ${WEBSITEDIR}/
	cp -a web ${WEBSITEDIR}/
	cd ${WEBSITEDIR}/web/pages; git init .;	git add index.md; \
	git commit -m "imported index.md"

.PHONY: deploy-clean
deploy-clean:
	[ "${WEBSITEDIR}" != "/" ] && rm -rf "${WEBSITEDIR}"