.PHONY: verify verify-static verify-template check ci fmt lint lint-version-check test race cross tidy corrections-base build run app dev install uninstall linux-only refresh-desktop snapshot live

GOCMD ?= go

# Pocket builds without CGO everywhere: MyGo loads GTK, OpenGL and the
# platform's libraries at run time with purego.
CGO_ENABLED = 0

# Pinned golangci-lint release, read from mise.toml, the single source of
# every tool pin: `mise install` provisions it locally and CI installs it with
# jdx/mise-action, verified against mise.lock. Compared with `golangci-lint
# version --short`, which prints the bare "MAJOR.MINOR.PATCH".
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
# The Go release from go.mod's go line, the only Go pin. golangci-lint must be
# built with a Go of at least this language version (MAJOR.MINOR).
GO_VERSION := $(strip $(shell sed -n 's/^go \(.*\)/\1/p' go.mod))
GO_LANGUAGE := $(shell echo "$(GO_VERSION)" | cut -d. -f1,2)
# The major version of the Node on PATH (mise.toml pins it): the template gate
# runs on it.
NODE_MAJOR := $(shell node -p "process.versions.node.split('.')[0]" 2>/dev/null)

# The GOOS/GOARCH pairs Pocket builds for. make cross builds and vets each
# pair; make ci runs make cross, so CI's verify job checks every pair.
CROSS_TARGETS ?= linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

# Where make snapshot and make live write the views as PNGs.
SNAPSHOT_DIR ?= build/snapshots

# Where make install puts Pocket, on Linux: the app in PREFIX/lib/pocket, its
# command in PREFIX/bin, its desktop entry and icons in the user's data
# folder. The entry and the icons are named after the app's identifier
# (mygo.json), which is the app ID of Pocket's windows.
PREFIX ?= $(HOME)/.local
DATA_HOME := $(if $(XDG_DATA_HOME),$(XDG_DATA_HOME),$(HOME)/.local/share)
APP_ID := $(strip $(shell sed -n 's/^ *"identifier": *"\(.*\)",*$$/\1/p' mygo.json))
INSTALL_APP = $(PREFIX)/lib/pocket
INSTALL_BIN = $(PREFIX)/bin/pocket
INSTALL_ENTRY = $(DATA_HOME)/applications/$(APP_ID).desktop
INSTALL_ICONS = $(DATA_HOME)/icons/hicolor
# What make app builds for this machine, and the list of the app's files
# that MyGo writes there.
APP_OUT = build/mygo/linux-$(shell $(GOCMD) env GOARCH)
APP_FILES = .mygo-files

# The tests need no display: MyGo's Tester draws on the CPU with Pango and
# cairo. Unsetting DISPLAY and WAYLAND_DISPLAY proves it on a desktop too.
HEADLESS = env -u DISPLAY -u WAYLAND_DISPLAY

fmt:
	gofmt -s -w .

tidy:
	$(GOCMD) mod tidy

test:
	$(HEADLESS) CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) test ./...

# The race detector needs CGO and a C compiler.
race:
	$(HEADLESS) CGO_ENABLED=1 $(GOCMD) test -race -short ./...

# Build and vet every package, tests included, for each target.
cross:
	@for target in $(CROSS_TARGETS); do \
		goos=$${target%/*}; goarch=$${target#*/}; \
		echo "==> cross: $$goos/$$goarch"; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GOCMD) build ./... || exit 1; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GOCMD) vet ./... || exit 1; \
	done

# Lint (fails when the mise.toml pin is missing or the installed release
# differs), as the host and as macOS and Windows, which Pocket ships to.
lint:
	$(MAKE) --no-print-directory lint-version-check
	CGO_ENABLED=$(CGO_ENABLED) golangci-lint run
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=$(CGO_ENABLED) golangci-lint run
	GOOS=windows GOARCH=amd64 CGO_ENABLED=$(CGO_ENABLED) golangci-lint run

# Fail unless the installed golangci-lint is the mise.toml pin and was built
# with a Go no older than go.mod's go line
lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (not installed; run: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (run: mise install)"; \
		exit 1; \
	fi; \
	built="$$(golangci-lint version 2>/dev/null | sed -n 's/.*built with go\([0-9]*\.[0-9]*\).*/\1/p')"; \
	if [ -n "$$built" ] && [ "$$(printf '%s\n%s\n' "$(GO_LANGUAGE)" "$$built" | sort -V | head -1)" != "$(GO_LANGUAGE)" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) was built with go$$built, older than go.mod's go$(GO_LANGUAGE): bump golangci-lint first"; \
		exit 1; \
	fi

# Check the agent surfaces, docs index, risk paths, corrections and vendored
# copies (needs Node.js 22 or later, from mise.toml)
verify-template:
	@if ! command -v node >/dev/null 2>&1; then echo "FAIL: make verify requires Node.js 22 or later and make; run: mise install"; exit 1; fi
	@if [ "$(NODE_MAJOR)" = "" ] || [ "$(NODE_MAJOR)" -lt 22 ]; then echo "FAIL: make verify requires Node.js 22 or later and make (found: $$(node --version 2>/dev/null || echo missing)); run: mise install"; exit 1; fi
	node scripts/check-template.mjs
	node --test test/*.test.mjs

# The non-mutating static checks: tidy diff, vet, MyGo's build-lifetime vet,
# gofmt, the pinned lint and the template gate
verify-static:
	@echo "==> verify: go.mod is tidy"
	$(GOCMD) mod tidy -diff
	@echo "==> verify: go vet"
	CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) vet ./...
	@echo "==> verify: mygo vet (native UI build lifetimes)"
	$(GOCMD) tool mygo vet .
	@echo "==> verify: gofmt"
	test -z "$$(gofmt -s -l .)"
	@echo "==> verify: lint"
	$(MAKE) --no-print-directory lint
	@echo "==> verify: template, docs index, risk paths, corrections, vendored copies"
	$(MAKE) --no-print-directory verify-template

# The credential-free gate that leaves git status unchanged: verify-static,
# then every test. The live tests skip unless their servers are set.
verify: verify-static
	@echo "==> verify: tests"
	$(MAKE) --no-print-directory test

# Developer gate: format, then verify
check: fmt verify

# What CI's verify job runs, in fail-fast order: verify, the corrections
# check against BASE when it is set (make ci BASE=origin/main), the race
# detector and the cross-build of every target.
ci: verify
	$(MAKE) --no-print-directory corrections-base
	@echo "==> ci: race detector"
	$(MAKE) --no-print-directory race
	@echo "==> ci: cross-build"
	$(MAKE) --no-print-directory cross
	@echo "==> ci passed"

# .memory/corrections/ is checked against BASE (no record removed or renamed,
# only an empty promoted_to filled, no old corrections file). A BASE that does not
# resolve to a commit is an error, never a silent pass.
corrections-base:
	@if [ -z "$(BASE)" ]; then exit 0; fi; \
	if ! git rev-parse --verify --quiet "$(BASE)^{commit}" >/dev/null; then \
		echo "FAIL: BASE $(BASE) does not resolve to a commit (fetch it first)"; \
		exit 1; \
	fi; \
	node scripts/check-corrections.mjs --base "$(BASE)"

# Build the executable for this machine into build/pocket.
build:
	CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) build -o build/pocket ./cmd/pocket

# Build and start the app.
run:
	CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) run ./cmd/pocket

# The app as people install it, for this machine: Pocket.app and a disk
# image on macOS (signed ad hoc unless mygo.json names an identity), the
# executable and packages on Linux. Output in build/mygo.
app:
	$(GOCMD) tool mygo build

# A development build, packaged so that macOS shows its notifications.
dev:
	$(GOCMD) tool mygo dev

# Fail unless this is Linux, where make install and make uninstall work.
linux-only:
	@[ "$$(uname -s)" = Linux ] || { echo "make install and make uninstall are for Linux (found $$(uname -s)); on macOS, make app builds Pocket.app"; exit 1; }
	@test -n "$(APP_ID)" || { echo "mygo.json names no identifier"; exit 1; }
	@test -n "$(PREFIX)" && test -n "$(HOME)" || { echo "PREFIX and HOME must be set"; exit 1; }

# Install the app for this user, on Linux: the production build of make app
# (the executable with the terminal's library beside it) in INSTALL_APP, a
# link to it in PREFIX/bin, the icons in the hicolor theme at the sizes MyGo
# draws them, and a desktop entry. StartupWMClass is the identifier, which
# cmd/pocket gives the windows as their app ID. The list of the app's files
# goes with them, for make uninstall.
install: linux-only app
	@test -x $(APP_OUT)/pocket || { echo "make app left no $(APP_OUT)/pocket"; exit 1; }
	@if [ -e "$(INSTALL_BIN)" ] && [ ! -L "$(INSTALL_BIN)" ]; then echo "$(INSTALL_BIN) is not make install's link: move it away first"; exit 1; fi
	@mkdir -p "$(INSTALL_APP)" "$(dir $(INSTALL_BIN))" "$(dir $(INSTALL_ENTRY))"
	@grep -v -x -e pocket.desktop -e pocket.png $(APP_OUT)/$(APP_FILES) > "$(INSTALL_APP)/$(APP_FILES)"
	@while IFS= read -r f; do \
		cp -p "$(APP_OUT)/$$f" "$(INSTALL_APP)/$$f" || exit 1; \
		echo "installed $(INSTALL_APP)/$$f"; \
	done < "$(INSTALL_APP)/$(APP_FILES)"
	@ln -sfn "$(INSTALL_APP)/pocket" "$(INSTALL_BIN)"
	@echo "installed $(INSTALL_BIN)"
	@for icon in $(APP_OUT)/pocket-*x*.png; do \
		size=$${icon##*/pocket-}; size=$${size%.png}; \
		install -Dm644 "$$icon" "$(INSTALL_ICONS)/$$size/apps/$(APP_ID).png" || exit 1; \
		echo "installed $(INSTALL_ICONS)/$$size/apps/$(APP_ID).png"; \
	done
	@printf '%s\n' '[Desktop Entry]' 'Type=Application' 'Name=Pocket' \
		'Comment=One window for a team of agents' \
		'Exec="$(INSTALL_BIN)"' 'Icon=$(APP_ID)' 'Categories=Development;' \
		'Terminal=false' 'StartupWMClass=$(APP_ID)' > "$(INSTALL_ENTRY)"
	@echo "installed $(INSTALL_ENTRY)"
	@$(MAKE) --no-print-directory refresh-desktop
	@echo "Installed Pocket: open it from the applications menu, or run $(INSTALL_BIN)"

# Remove the files make install wrote, one by one, and nothing else: the
# settings and the token in the keyring stay.
uninstall: linux-only
	@if [ -f "$(INSTALL_APP)/$(APP_FILES)" ]; then \
		while IFS= read -r f; do rm -f "$(INSTALL_APP)/$$f"; done < "$(INSTALL_APP)/$(APP_FILES)"; \
		rm -f "$(INSTALL_APP)/$(APP_FILES)"; \
		rmdir "$(INSTALL_APP)" || echo "left $(INSTALL_APP): it holds files make install did not write"; \
	fi
	@if [ -L "$(INSTALL_BIN)" ] && [ "$$(readlink "$(INSTALL_BIN)")" = "$(INSTALL_APP)/pocket" ]; then \
		rm -f "$(INSTALL_BIN)"; \
	elif [ -e "$(INSTALL_BIN)" ] || [ -L "$(INSTALL_BIN)" ]; then \
		echo "left $(INSTALL_BIN): make install did not write it"; \
	fi
	@rm -f "$(INSTALL_ENTRY)" "$(INSTALL_ICONS)"/*/apps/$(APP_ID).png
	@$(MAKE) --no-print-directory refresh-desktop
	@echo "Removed Pocket from $(PREFIX) and $(DATA_HOME)"

# Tell the desktop about a changed entry or icon, with the tools it has.
refresh-desktop:
	@if command -v update-desktop-database >/dev/null 2>&1; then update-desktop-database -q "$(dir $(INSTALL_ENTRY))" || true; fi
	@if command -v gtk-update-icon-cache >/dev/null 2>&1; then gtk-update-icon-cache -q -t -f "$(INSTALL_ICONS)" || true; fi

# Write the views, light and dark, as PNGs in SNAPSHOT_DIR.
snapshot:
	@mkdir -p $(SNAPSHOT_DIR)
	POCKET_SNAPSHOT_DIR=$(abspath $(SNAPSHOT_DIR)) $(HEADLESS) CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) test ./internal/app -run 'Snapshot' -count=1
	POCKET_DARK=1 POCKET_SNAPSHOT_DIR=$(abspath $(SNAPSHOT_DIR)) $(HEADLESS) CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) test ./internal/app -run 'TeamSnapshots' -count=1

# The live tests against real Pi Pocket servers: set POCKET_TEST_SERVER and
# POCKET_TEST_TOKEN, and POCKET_DEMO_SERVER and POCKET_DEMO_TOKEN (README).
live:
	@mkdir -p $(SNAPSHOT_DIR)
	POCKET_SNAPSHOT_DIR=$(abspath $(SNAPSHOT_DIR)) $(HEADLESS) CGO_ENABLED=$(CGO_ENABLED) $(GOCMD) test ./internal/app -run 'TestLive' -count=1 -v
