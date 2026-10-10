# Builds the MBT manuals.  Needs typst and the bookmaster submodule
# (git submodule update --init docs/books/bookmaster).

TYPST ?= typst
BM    := bookmaster
# The PDF carries the edition: ml02-0001-0.pdf is the Draft of ML02-0001.
BOOKS := ml02-0001-0.pdf ml02-0002-0.pdf

.PHONY: all clean site

all: $(BOOKS)

ml02-0001-%.pdf: ml02-0001.typ $(wildcard guide/*.typ) $(BM)/bookmaster.typ
	$(TYPST) compile --root . --font-path $(BM)/fonts --ignore-system-fonts $< $@

ml02-0002-%.pdf: ml02-0002.typ $(wildcard ref/*.typ) $(BM)/bookmaster.typ
	$(TYPST) compile --root . --font-path $(BM)/fonts --ignore-system-fonts $< $@

# The web form, as Read the Docs builds it, into site/.
site:
	sh web/build.sh site

clean:
	rm -rf *.pdf site
