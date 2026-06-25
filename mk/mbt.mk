# mk/mbt.mk -- cc370 toolchain build system for mbt v2
#
# All mvslovers projects use the same 2-line Makefile:
#
#   MBT_ROOT := mbt
#   include $(MBT_ROOT)/mk/mbt.mk
#
# Project configuration lives in project.toml.
# Build runs entirely on the host via cc370/as370/ld370.

# -- Paths ---------------------------------------------------------
MBT_SCRIPTS := $(MBT_ROOT)/scripts
MBT_INCLUDE := $(MBT_ROOT)/include
BUILDDIR    ?= build
DISTDIR     ?= dist

# -- Toolchain defaults --------------------------------------------
# GNU Make pre-defines CC/AS/LD/AR (origin "default"), so a plain ?=
# would NOT override them -- the build would silently run the host's
# cc/as/ld/ar instead of the cc370 cross-toolchain.  Override the
# built-in defaults (and the undefined case) but still honour an
# explicit value from the environment or command line.
ifneq ($(filter undefined default,$(origin CC)),)
CC := cc370
endif
ifneq ($(filter undefined default,$(origin AS)),)
AS := as370
endif
ifneq ($(filter undefined default,$(origin LD)),)
LD := ld370
endif
ifneq ($(filter undefined default,$(origin AR)),)
AR := ar370
endif
FILE370 ?= file370

# ':=' (not '?=') so a host CFLAGS/ASFLAGS/LDFLAGS in the environment does
# NOT leak into the cross-build (e.g. a Homebrew LDFLAGS=-L.../libiconv).
# A 'make CFLAGS=...' on the command line still overrides these.
CFLAGS  := -O1
ASFLAGS :=
LDFLAGS :=

# -- Verbosity -----------------------------------------------------
# 'VERBOSE=1 make' echoes every cc370/as370/ld370/ar370 invocation with
# its full arguments instead of the short [tool] labels, and passes
# --verbose down to the deploy script.
ifeq ($(VERBOSE),1)
  Q :=
  E := @:
  VFLAG := --verbose
else
  Q := @
  E := @echo
  VFLAG :=
endif

# -- Sysroot (libc370 headers/libs/macros) -------------------------
# cc370 locates its own headers/libs relative to its binary
# (<bindir>/../cc370), so derive the sysroot the same way.  Note:
# 'cc370 -print-search-dirs' reports the configure-time install prefix,
# which is wrong for a relocated toolchain -- don't rely on it.
CC_BIN := $(shell command -v $(CC) 2>/dev/null)
ifneq ($(CC_BIN),)
  SYSROOT := $(abspath $(dir $(CC_BIN))/../cc370)
endif
# Fall back to the default install location if the derived sysroot does
# not actually contain the crt objects.
ifeq ($(wildcard $(SYSROOT)/lib/crt0.o),)
  SYSROOT := $(HOME)/.local/cc370
endif

CRT0 := $(SYSROOT)/lib/crt0.o
CRT1 := $(SYSROOT)/lib/crt1.o
CRTM := $(SYSROOT)/lib/crtm.o

# ld370 has no built-in library search path, so the sysroot lib dir must
# be passed explicitly for -lc (libc370) to resolve.
LDLIBDIR := -L$(SYSROOT)/lib

# -- Read project.toml -> .mbt/config.mk --------------------------
$(shell mkdir -p .mbt $(BUILDDIR))
$(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --builddir $(BUILDDIR) \
    --output file 2>&1 | grep -v '^\[mbt\]' >&2)

-include .mbt/config.mk

# -- Dependency artifacts (staged by 'make deps' into .mbt/deps) ----
# Each dep is .mbt/deps/<repo>/{include,lib}: add its headers to the
# compile include path and its archive(s) to the link.
DEP_INCLUDES := $(addprefix -I ,$(wildcard .mbt/deps/*/include))
DEP_LIBS     := $(wildcard .mbt/deps/*/lib/*.a)
CFLAGS       += $(DEP_INCLUDES)

# mbt's own headers (mbtcheck.h test convention) -- always on the include path
# so tests can '#include <mbtcheck.h>'; harmless for sources that don't.
CFLAGS       += -I $(MBT_INCLUDE)

# -- VPATH for source discovery ------------------------------------
vpath %.c $(SRC_DIRS)
vpath %.asm $(SRC_DIRS)
vpath %.s $(SRC_DIRS)

# -- Header dependency tracking ------------------------------------
# cc370 (-MMD -MP) writes a .d file listing each object's header
# prerequisites, so editing a header rebuilds the dependent objects.
# The source/member names contain '#' (MVS member convention), which
# make treats as a comment -- cc370 does NOT escape it in the .d, so
# the recipe post-processes each .d to escape every '#'.
DEPFLAGS := -MMD -MP

# -- Pattern rules -------------------------------------------------
$(BUILDDIR)/%.o: %.c
	$(E) "[cc370] $<"
	$(Q)$(CC) $(CFLAGS) $(DEPFLAGS) -c $< -o $@
	$(Q)d="$@"; d="$${d%.o}.d"; sed 's/#/\\#/g' "$$d" > "$$d.e" && mv "$$d.e" "$$d"

$(BUILDDIR)/%.o: %.asm
	$(E) "[as370] $<"
	$(Q)$(AS) $(ASFLAGS) -o $@ $<

$(BUILDDIR)/%.o: %.s
	$(E) "[as370] $<"
	$(Q)$(AS) $(ASFLAGS) -o $@ $<

# -- Link helpers --------------------------------------------------
# Called by the generated module rules below.
# $(1) = entry, $(2) = name, $(3) = objects, $(4) = AC, $(5) = norent, $(6) = noreus
# AC/norent/noreus are passed by VALUE (looked up by the make-safe key in the
# rule) so they resolve even for a module name carrying '#'.

define LINK_CRT0
	$(E) "[ld370] $(2) (entry=$(1), crt0)"
	$(Q)$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRT0) $(3) $(INTERNAL_ARCHIVE) $(DEP_LIBS) -lc $(if $(4),--ac $(4) ,)$(if $(5),--norent ,)$(if $(6),--noreus ,)-iebcopy -o $(BUILDDIR)/$(2)
endef

define LINK_CRT1
	$(E) "[ld370] $(2) (entry=$(1), crt1)"
	$(Q)$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRT1) $(3) $(INTERNAL_ARCHIVE) $(DEP_LIBS) -lc $(if $(4),--ac $(4) ,)$(if $(5),--norent ,)$(if $(6),--noreus ,)-iebcopy -o $(BUILDDIR)/$(2)
endef

define LINK_CRTM
	$(E) "[ld370] $(2) (entry=$(1), crtm)"
	$(Q)$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRTM) $(3) $(INTERNAL_ARCHIVE) $(DEP_LIBS) -lc $(if $(4),--ac $(4) ,)$(if $(5),--norent ,)$(if $(6),--noreus ,)-iebcopy -o $(BUILDDIR)/$(2)
endef

define LINK_NOCRT
	$(E) "[ld370] $(2) (entry=$(1), no crt)"
	$(Q)$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(3) $(INTERNAL_ARCHIVE) $(DEP_LIBS) -lc $(if $(4),--ac $(4) ,)$(if $(5),--norent ,)$(if $(6),--noreus ,)-iebcopy -o $(BUILDDIR)/$(2)
endef

# -- Auto-generate link rules for each module/test ----------------
# Each module links to a per-module IEBCOPY unload (build/NAME.iebcopy):
# unlike a bare load module it carries the PDS2 directory (entry point +
# module length), so 'deploy' can ld370 --pack them into one LINKLIB XMIT.
# For each MODULE and TEST, create:
#   build/NAME.iebcopy: build/obj1.o build/obj2.o ...
#       $(call LINK_xxx, ENTRY, NAME, $^, AC, NORENT, NOREUS)
#   name (lowercase): build/NAME.iebcopy    <- alias

# $(1) is the make-safe key; the real member name is MODULE_$(1)_NAME (may
# contain national chars like '#').  The '#' only ever reaches a target/recipe
# via variable expansion -- after comment stripping -- so it stays literal.
define _MODULE_RULE
$(BUILDDIR)/$$(MODULE_$(1)_NAME).iebcopy: $$(MODULE_$(1)_OBJS) $(INTERNAL_ARCHIVE)
	$$(call $$(MODULE_$(1)_LINK_CMD),$$(MODULE_$(1)_ENTRY),$$(MODULE_$(1)_NAME),$$(MODULE_$(1)_OBJS),$$(MODULE_$(1)_AC),$$(MODULE_$(1)_NORENT),$$(MODULE_$(1)_NOREUS))

.PHONY: $$(MODULE_$(1)_ALIAS)
$$(MODULE_$(1)_ALIAS): $(BUILDDIR)/$$(MODULE_$(1)_NAME).iebcopy
endef

$(foreach m,$(MODULES),$(eval $(call _MODULE_RULE,$(m))))
$(foreach t,$(TESTS),$(eval $(call _MODULE_RULE,$(t))))

# -- Per-module IEBCOPY unload lists -------------------------------
MODULE_IMGS := $(foreach m,$(MODULES),$(BUILDDIR)/$(MODULE_$(m)_NAME).iebcopy)
TEST_IMGS   := $(foreach t,$(TESTS),$(BUILDDIR)/$(MODULE_$(t)_NAME).iebcopy)

# -- Include generated header dependencies -------------------------
# Missing on the first build (-include ignores them); present and
# escaped after each object compiles.
-include $(ALL_OBJS:.o=.d)

# -- Library target ------------------------------------------------
# A [lib] with compiled members produces a static archive (build/<name>.a).
# A [lib] with only `headers` and no `sources` is a *headers-only* export --
# the public API is reached at runtime (e.g. a callback table the host fills
# in), so consumers compile against the headers but link nothing.  No archive
# is built or shipped in that case; LIB_FILE stays empty.
ifdef LIB_NAME
ifneq ($(strip $(LIB_OBJS)),)
LIB_FILE := $(BUILDDIR)/$(LIB_NAME).a

$(LIB_FILE): $(LIB_OBJS)
	$(E) "[ar370] $(LIB_NAME).a ($(words $^) objects)"
	$(Q)$(AR) rc $@ $^
endif
endif

# -- Internal autocall archive -------------------------------------
# Project-private archive of all shared objects (from [internal] sources).
# Every module/test autocalls it (added to the LINK_* command line), so a
# module need only list its own root source(s) and the linker pulls the
# shared rest by autocall.  Not a deliverable -- never packaged or shipped.
ifdef INTERNAL_ARCHIVE
$(INTERNAL_ARCHIVE): $(INTERNAL_OBJS)
	$(E) "[ar370] $(notdir $(INTERNAL_ARCHIVE)) ($(words $^) objects)"
	$(Q)$(AR) rc $@ $^
endif

# -- Standard targets ----------------------------------------------
# Bare `make` builds the project's PRIMARY deliverable; `make all` builds
# everything it declares.  Driven by [project] type:
#   library             -> primary = the static archive (no load modules)
#   application/runtime -> primary = the load modules; `all` adds [lib]
# Set the default goal explicitly here -- the per-module rules above are
# generated via $(eval) inside a foreach, so the first one (e.g.
# build/UFSD.iebcopy) would otherwise become the default goal.
ifeq ($(PROJECT_TYPE),library)
.DEFAULT_GOAL := lib
ALL_PREREQS   := lib
else
.DEFAULT_GOAL := modules
ALL_PREREQS   := modules $(if $(LIB_NAME),lib)
endif

.PHONY: all modules test test-mvs test-host check lib package deps deploy doctor compiledb release \
        prerelease clean distclean help

# Help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build:"
	@echo "  (bare make)  Build the primary deliverable for this type"
	@echo "               (library: the archive; else: load modules)"
	@echo "  all          Build everything (modules + library archive)"
	@echo "  modules      Build production modules only"
	@echo "  test         Build test modules"
	@echo "  lib          Build library archive"
	@echo ""
	@echo "Modules:"
	@$(foreach m,$(MODULES),echo "  $(MODULE_$(m)_ALIAS)";)
	@echo ""
	@echo "Tests:"
	@$(foreach t,$(TESTS),echo "  $(MODULE_$(t)_ALIAS)";)
	@echo ""
	@echo "Package & Release:"
	@echo "  package      Create release artifacts in dist/"
	@echo "  deploy       Upload XMITs to MVS and RECV370"
	@echo "  test-host    Build + run the dual tests natively (fast inner loop)"
	@echo "  test-mvs     Build + deploy test modules + run the suite on MVS"
	@echo "  check        Run every available test suite (host + MVS)"
	@echo "  release      Version bump + git tag + GH release"
	@echo "  prerelease   Prerelease (no version bump)"
	@echo ""
	@echo "Other:"
	@echo "  deps         Download declared dependencies (like old bootstrap)"
	@echo "  doctor       Check toolchain and connectivity"
	@echo "  compiledb    Write compile_commands.json for clangd"
	@echo "  clean        Remove build/ dist/ (keeps staged deps)"
	@echo "  distclean    clean + remove all of .mbt/ (incl. deps)"
	@echo "  help         Show this message"
	@echo ""
	@echo "Options:"
	@echo "  VERBOSE=1    Show full cc370/as370/ld370/ar370 commands"
	@echo "               (e.g. 'VERBOSE=1 make')"

# Build everything the project declares (see ALL_PREREQS above)
all: $(ALL_PREREQS)
	@echo "[mbt] Build complete"

# Build production modules only
modules: $(MODULE_IMGS)
	@echo "[mbt] Modules built: $(words $(MODULES))"

# Build test modules
test: $(TEST_IMGS)
	@echo "[mbt] Tests built: $(words $(TESTS)) module(s)"

# Build library archive (or just report headers for a headers-only [lib])
ifdef LIB_NAME
lib: $(LIB_FILE)
	@echo "[mbt] Library: $(if $(LIB_FILE),$(LIB_FILE),$(LIB_NAME) (headers only, $(words $(LIB_HEADERS)) header(s)))"
else
lib:
	@echo "[mbt] No [lib] section in project.toml"
endif

# -- Packaging -----------------------------------------------------
DIST_PREFIX := $(PROJECT_NAME)-$(PROJECT_VERSION)

package: modules $(if $(LIB_NAME),lib)
	@mkdir -p $(DISTDIR)
# Load archive (per-module IEBCOPY unloads). Skipped for pure-library
# projects (no [[module]] blocks): an empty file list would make tar fail
# with "no files or directories specified" and abort the whole target.
ifneq ($(strip $(MODULES)),)
	@echo "[mbt] Packaging $(DIST_PREFIX)-load.tar.gz"
	@tar czf $(DISTDIR)/$(DIST_PREFIX)-load.tar.gz \
	    -C $(BUILDDIR) $(foreach m,$(MODULES),$(MODULE_$(m)_NAME).iebcopy)
endif
ifdef LIB_NAME
	@# Library export: headers always; the .a only if the [lib] has members
	@# (a headers-only [lib] ships include/ alone -- see the lib target above).
	@echo "[mbt] Packaging $(DIST_PREFIX)-lib.tar.gz"
	@mkdir -p $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/include
	$(if $(LIB_FILE),@mkdir -p $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/lib && cp $(LIB_FILE) $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/lib/)
	@$(foreach h,$(LIB_HEADERS),cp $(h) $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/include/;)
	@tar czf $(DISTDIR)/$(DIST_PREFIX)-lib.tar.gz \
	    -C $(BUILDDIR)/pkg-lib $(DIST_PREFIX)
	@rm -rf $(BUILDDIR)/pkg-lib
endif
	@echo "[mbt] Package complete -> $(DISTDIR)/"

# -- Dependencies (download + stage declared deps into .mbt/deps) ---
# Resolve each [dependencies] entry, download its {repo}-{ver}-lib.tar.gz,
# stage headers + .a into .mbt/deps/<repo>/, and lock the SHA. Run before
# 'make' for projects with dependencies. ARGS=--update re-resolves ranges.
deps:
	@python3 $(MBT_SCRIPTS)/mbtdeps.py --project project.toml $(ARGS)

# -- Deploy (pack built load modules -> XMIT -> MVS -> RECEIVE) ----
# No 'modules' prerequisite on purpose: deploy packs whatever is already
# built in $(BUILDDIR) (so 'make ufsd && make deploy' deploys only UFSD,
# 'make && make deploy' deploys all).
deploy:
	@python3 $(MBT_SCRIPTS)/mbtdeploy.py --project project.toml \
	    --builddir $(BUILDDIR) --ld $(LD) $(VFLAG) $(ARGS)

# -- test-mvs (build test modules, deploy to a TESTLIB, run them on MVS) --
# Depends on 'test' so the modules are current; packs them into a separate
# TESTLIB, generates + submits a runner job (batch + TSO per test), and reports
# a pass/fail matrix.  The production LINKLIB must already be deployed (tests
# LOAD data modules from it) -- the runner fails fast if it is absent.
test-mvs: test
	@python3 $(MBT_SCRIPTS)/mbttest.py --project project.toml \
	    --builddir $(BUILDDIR) --ld $(LD) $(VFLAG) $(ARGS)

# -- test-host (build + run the dual tests natively -- fast inner loop) --
# Compiles every [[test]] whose sources are portable C with the host compiler
# and runs it, gating on the exit code.  No MVS; tests carrying .asm are skipped.
test-host:
	@python3 $(MBT_SCRIPTS)/mbttesthost.py --project project.toml \
	    --builddir $(BUILDDIR) $(VFLAG) $(ARGS)

# -- check (run every available test suite: host first, then MVS) --
check: test-host test-mvs
	@:

# -- Doctor (check toolchain) -------------------------------------
doctor:
	@python3 $(MBT_SCRIPTS)/mbtdoctor.py --project project.toml

# -- compile_commands.json for clangd ------------------------------
compiledb:
	@python3 $(MBT_SCRIPTS)/mbtcompiledb.py --project project.toml

# -- Release management --------------------------------------------
prerelease: package
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml --prerelease

release: package
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml \
	    --version $(VERSION) \
	    $(if $(NEXT_VERSION),--next-version $(NEXT_VERSION),)

# -- Clean ---------------------------------------------------------
# clean keeps staged deps (.mbt/deps) so 'make clean && make' does not
# re-fetch; distclean wipes the whole .mbt/ (incl. deps). Neither touches
# mbt.lock -- it is committed source (project root), not a build artifact;
# 'make deps' re-stages from it after distclean.
clean:
	@echo "[mbt] Cleaning..."
	@rm -rf $(BUILDDIR)/ $(DISTDIR)/ .mbt/config.mk .mbt/logs/

distclean: clean
	@rm -rf .mbt/
