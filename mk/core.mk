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
# Write to temp file so $(eval) sees real newlines ($(shell) strips them).
$(shell mkdir -p .mbt)
$(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --output shell \
    > .mbt/config.mk 2>/dev/null)

-include .mbt/config.mk

# Load rules and targets
include $(MBT_ROOT)/mk/rules.mk
include $(MBT_ROOT)/mk/targets.mk
