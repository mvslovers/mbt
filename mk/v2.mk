# mk/v2.mk -- cc370 toolchain build system for mbt v2
#
# All mvslovers projects use the same 2-line Makefile:
#
#   MBT_ROOT := mbt
#   include $(MBT_ROOT)/mk/v2.mk
#
# Project configuration lives in project.toml.
# Build runs entirely on the host via cc370/as370/ld370.

# -- Paths ---------------------------------------------------------
MBT_SCRIPTS := $(MBT_ROOT)/scripts
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

CFLAGS  ?= -O1
ASFLAGS ?=
LDFLAGS ?=

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
$(shell python3 $(MBT_SCRIPTS)/mbtconfig_v2.py \
    --project project.toml --builddir $(BUILDDIR) \
    --output file 2>&1 | grep -v '^\[mbt\]' >&2)

-include .mbt/config.mk

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
	@echo "[cc370] $<"
	@$(CC) $(CFLAGS) $(DEPFLAGS) -c $< -o $@
	@d="$@"; d="$${d%.o}.d"; sed 's/#/\\#/g' "$$d" > "$$d.e" && mv "$$d.e" "$$d"

$(BUILDDIR)/%.o: %.asm
	@echo "[as370] $<"
	@$(AS) $(ASFLAGS) -o $@ $<

$(BUILDDIR)/%.o: %.s
	@echo "[as370] $<"
	@$(AS) $(ASFLAGS) -o $@ $<

# -- Link helpers --------------------------------------------------
# Called by the generated module rules below.
# $(1) = entry point, $(2) = module name, $(3) = object files

define LINK_CRT0
	@echo "[ld370] $(2) (entry=$(1), crt0)"
	@$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRT0) $(3) -lc -xmit -o $(BUILDDIR)/$(2)
endef

define LINK_CRT1
	@echo "[ld370] $(2) (entry=$(1), crt1)"
	@$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRT1) $(3) -lc -xmit -o $(BUILDDIR)/$(2)
endef

define LINK_CRTM
	@echo "[ld370] $(2) (entry=$(1), crtm)"
	@$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(CRTM) $(3) -lc -xmit -o $(BUILDDIR)/$(2)
endef

define LINK_NOCRT
	@echo "[ld370] $(2) (entry=$(1), no crt)"
	@$(LD) $(LDFLAGS) $(LDLIBDIR) -e $(1) $(3) -lc -xmit -o $(BUILDDIR)/$(2)
endef

# -- Auto-generate link rules for each module/test ----------------
# For each MODULE and TEST, create:
#   build/NAME.xmit: build/obj1.o build/obj2.o ...
#       $(call LINK_xxx, ENTRY, NAME, $^)
#   name (lowercase): build/NAME.xmit       <- alias

define _MODULE_RULE
$(BUILDDIR)/$(1).xmit: $$(MODULE_$(1)_OBJS)
	$$(call $$(MODULE_$(1)_LINK_CMD),$$(MODULE_$(1)_ENTRY),$(1),$$^)

.PHONY: $$(MODULE_$(1)_ALIAS)
$$(MODULE_$(1)_ALIAS): $(BUILDDIR)/$(1).xmit
endef

$(foreach m,$(MODULES),$(eval $(call _MODULE_RULE,$(m))))
$(foreach t,$(TESTS),$(eval $(call _MODULE_RULE,$(t))))

# -- Module and test XMIT file lists ------------------------------
MODULE_XMITS := $(addprefix $(BUILDDIR)/,$(addsuffix .xmit,$(MODULES)))
TEST_XMITS   := $(addprefix $(BUILDDIR)/,$(addsuffix .xmit,$(TESTS)))

# -- Include generated header dependencies -------------------------
# Missing on the first build (-include ignores them); present and
# escaped after each object compiles.
-include $(ALL_OBJS:.o=.d)

# -- Library target ------------------------------------------------
ifdef LIB_NAME
LIB_FILE := $(BUILDDIR)/$(LIB_NAME).a

$(LIB_FILE): $(LIB_OBJS)
	@echo "[ar370] $(LIB_NAME).a ($(words $^) objects)"
	@$(AR) rc $@ $^
endif

# -- Standard targets ----------------------------------------------
# The per-module rules above are generated via $(eval) inside a foreach,
# so the first one (build/UFSD.xmit) would otherwise become the default
# goal.  Force 'all' to be the default for a bare 'make'.
.DEFAULT_GOAL := all

.PHONY: all modules test lib package deps deploy doctor release prerelease \
        clean distclean help

# Help
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build:"
	@echo "  all          Build all modules (default)"
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
	@echo "  release      Version bump + git tag + GH release"
	@echo "  prerelease   Prerelease (no version bump)"
	@echo ""
	@echo "Other:"
	@echo "  deps         Download declared dependencies (like old bootstrap)"
	@echo "  doctor       Check toolchain and connectivity"
	@echo "  clean        Remove build/ dist/ .mbt/"
	@echo "  distclean    clean + remove deps/"
	@echo "  help         Show this message"

# Default: build all modules
all: modules
	@echo "[mbt] Build complete: $(words $(MODULES)) module(s)"

# Build production modules only
modules: $(MODULE_XMITS)

# Build test modules
test: $(TEST_XMITS)
	@echo "[mbt] Tests built: $(words $(TESTS)) module(s)"

# Build library archive
ifdef LIB_NAME
lib: $(LIB_FILE)
	@echo "[mbt] Library: $(LIB_FILE)"
else
lib:
	@echo "[mbt] No [lib] section in project.toml"
endif

# -- Packaging -----------------------------------------------------
DIST_PREFIX := $(PROJECT_NAME)-$(PROJECT_VERSION)

package: modules $(if $(LIB_NAME),lib)
	@mkdir -p $(DISTDIR)
	@# Load archive (module XMITs)
	@echo "[mbt] Packaging $(DIST_PREFIX)-load.tar.gz"
	@tar czf $(DISTDIR)/$(DIST_PREFIX)-load.tar.gz \
	    -C $(BUILDDIR) $(foreach m,$(MODULES),$(m).xmit)
ifdef LIB_NAME
	@# Library archive (lib + headers)
	@echo "[mbt] Packaging $(DIST_PREFIX)-lib.tar.gz"
	@mkdir -p $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/lib \
	          $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/include
	@cp $(LIB_FILE) $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/lib/
	@$(foreach h,$(LIB_HEADERS),cp $(h) $(BUILDDIR)/pkg-lib/$(DIST_PREFIX)/include/;)
	@tar czf $(DISTDIR)/$(DIST_PREFIX)-lib.tar.gz \
	    -C $(BUILDDIR)/pkg-lib $(DIST_PREFIX)
	@rm -rf $(BUILDDIR)/pkg-lib
endif
	@echo "[mbt] Package complete -> $(DISTDIR)/"

# -- Dependencies (download declared deps, like old 'make bootstrap') --
# TODO: implement download/RECEIVE of declared dependencies in a
# dedicated mbtdeps.py (deferred to the first v2 project that actually
# has dependencies; ufsd v2 has none).
deps:
	@python3 -c "import tomllib; \
d=tomllib.load(open('project.toml','rb')).get('dependencies',{}); \
print('[mbt] Dependencies:', ', '.join(d) if d else 'none declared')"
	@echo "[mbt] Note: dependency download not implemented yet (mbtdeps.py)."

# -- Deploy (upload XMIT -> MVS -> RECV370) -----------------------
deploy: modules
	@python3 $(MBT_SCRIPTS)/mbtdeploy.py --project project.toml \
	    --builddir $(BUILDDIR) $(ARGS)

# -- Doctor (check toolchain) -------------------------------------
doctor:
	@python3 $(MBT_SCRIPTS)/mbtdoctor_v2.py --project project.toml

# -- Release management --------------------------------------------
prerelease: package
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml --prerelease

release: package
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml \
	    --version $(VERSION) \
	    $(if $(NEXT_VERSION),--next-version $(NEXT_VERSION),)

# -- Clean ---------------------------------------------------------
clean:
	@echo "[mbt] Cleaning..."
	@rm -rf $(BUILDDIR)/ $(DISTDIR)/ .mbt/

distclean: clean
	@rm -rf deps/
