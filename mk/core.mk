# mk/core.mk — Main include file for consumer projects.
#
# Consumer Makefile needs only:
#   MBT_ROOT := path/to/mbt
#   include $(MBT_ROOT)/mk/core.mk

# Resolve paths
MBT_SCRIPTS := $(MBT_ROOT)/scripts
MBT_BIN     := $(MBT_ROOT)/bin

# Load defaults
include $(MBT_ROOT)/mk/defaults.mk

# Load config from Python (single invocation).
# Errors go to stderr; BUILD_VARS is empty on failure.
BUILD_VARS := $(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --output shell 2>/dev/null)

ifdef BUILD_VARS
$(eval $(BUILD_VARS))
endif

# Load rules and targets
include $(MBT_ROOT)/mk/rules.mk
include $(MBT_ROOT)/mk/targets.mk
