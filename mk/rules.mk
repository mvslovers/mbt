# mk/rules.mk — Pattern rules
#
# Cross-compilation is handled in targets.mk _cross_compile.
# Assembly and MVS operations are handled by Python executors.
# This file is reserved for additional pattern rules if needed.

# Ensure asm output directory exists
$(shell mkdir -p asm)
