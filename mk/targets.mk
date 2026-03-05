# mk/targets.mk — Standard build targets

.PHONY: doctor bootstrap build link install package \
        release clean distclean graph datasets

doctor:
	@python3 $(MBT_SCRIPTS)/mbtdoctor.py --project project.toml

bootstrap:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml $(ARGS)

build: _cross_compile _assemble

_cross_compile:
	@for dir in $(C_DIRS); do \
	    for src in $$dir*.c; do \
	        [ -f "$$src" ] || continue; \
	        base=$$(basename $$src .c); \
	        echo "[mbt] Cross-compiling $$src..."; \
	        $(CC) $(CFLAGS) $(PROJECT_CFLAGS) $(INCLUDES) \
	            -o asm/$$base.s $$src || exit 1; \
	    done; \
	done

_assemble:
	@python3 $(MBT_SCRIPTS)/mvsasm.py --project project.toml

link:
	@python3 $(MBT_SCRIPTS)/mvslink.py --project project.toml

install:
	@python3 $(MBT_SCRIPTS)/mvsinstall.py --project project.toml

package:
	@python3 $(MBT_SCRIPTS)/mvspackage.py --project project.toml

release:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml $(VERSION)

graph:
	@python3 $(MBT_SCRIPTS)/mbtgraph.py --project project.toml

datasets:
	@python3 $(MBT_SCRIPTS)/mbtdatasets.py --project project.toml $(ARGS)

clean:
	@echo "[mbt] Cleaning build artifacts..."
	@rm -rf asm/*.s .mbt/logs/ dist/

distclean: clean
	@echo "[mbt] Deep clean..."
	@rm -rf contrib/ .mbt/

