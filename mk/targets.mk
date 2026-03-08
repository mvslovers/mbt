# mk/targets.mk — Standard build targets

.PHONY: doctor bootstrap bootstrap-datasets update-deps build link install \
        package prerelease release clean distclean graph datasets compiledb

doctor:
	@python3 $(MBT_SCRIPTS)/mbtdoctor.py --project project.toml

bootstrap:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml $(ARGS)

bootstrap-datasets:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml --datasets-only

update-deps:
	@python3 $(MBT_SCRIPTS)/mbtbootstrap.py --project project.toml --update

build:
	@python3 $(MBT_SCRIPTS)/mvsasm.py --project project.toml $(ARGS)

link:
	@python3 $(MBT_SCRIPTS)/mvslink.py --project project.toml $(ARGS)

install:
	@python3 $(MBT_SCRIPTS)/mvsinstall.py --project project.toml $(ARGS)

package:
	@python3 $(MBT_SCRIPTS)/mvspackage.py --project project.toml $(ARGS)

prerelease:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml --prerelease

release:
	@python3 $(MBT_SCRIPTS)/mvsrelease.py --project project.toml \
	    --version $(VERSION) \
	    $(if $(NEXT_VERSION),--next-version $(NEXT_VERSION),)

graph:
	@python3 $(MBT_SCRIPTS)/mbtgraph.py --project project.toml

datasets:
	@python3 $(MBT_SCRIPTS)/mbtdatasets.py --project project.toml $(ARGS)

compiledb:
	@python3 $(MBT_SCRIPTS)/mbtcompiledb.py --project project.toml

clean:
	@echo "[mbt] Cleaning build artifacts..."
	@for dir in $(C_DIRS); do rm -f "$$dir"/*.s "$$dir"/*.o; done
	@rm -rf .mbt/logs/ .mbt/stamps/ dist/

distclean: clean
	@echo "[mbt] Deep clean..."
	@rm -rf contrib/ .mbt/

