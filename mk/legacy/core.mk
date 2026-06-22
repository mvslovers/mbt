# mk/legacy/core.mk — LEGACY (v1, mvsMF/JCL) include file.
#
# Superseded by mk/mbt.mk (the cc370 host build).  Kept for projects not
# yet migrated; see docs/MIGRATION.md.
#
# Consumer Makefile needs only:
#   MBT_ROOT := path/to/mbt
#   include $(MBT_ROOT)/mk/legacy/core.mk

# Resolve paths (v1 scripts live under scripts/legacy/)
MBT_SCRIPTS := $(MBT_ROOT)/scripts/legacy
MBT_BIN     := $(MBT_ROOT)/bin

# Load defaults
include $(MBT_ROOT)/mk/legacy/defaults.mk

# Load config from Python (single invocation).
# Write to temp file so $(eval) sees real newlines ($(shell) strips them).
$(shell mkdir -p .mbt)
$(shell python3 $(MBT_SCRIPTS)/mbtconfig.py \
    --project project.toml --output shell \
    > .mbt/config.mk 2>/dev/null)

-include .mbt/config.mk

# Load rules and targets
include $(MBT_ROOT)/mk/legacy/rules.mk
include $(MBT_ROOT)/mk/legacy/targets.mk
