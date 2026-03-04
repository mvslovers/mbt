# mk/defaults.mk — Build defaults
#
# These can be overridden by the consumer Makefile before
# including core.mk.

CC       ?= c2asm370
CFLAGS   := -S -O1

# Convention directories
SRC_DIRS  ?= src/
ASM_DIRS  ?=
