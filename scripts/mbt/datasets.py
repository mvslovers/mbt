"""Dataset name computation.

Resolves dataset names from project config, version, HLQ,
and dependency information. Handles build datasets, dependency
datasets, install datasets, and CI build-ID overrides.
"""

from dataclasses import dataclass
from .config import MbtConfig
from .version import to_vrm

# Always present in every SYSLIB concatenation (MVS 3.8j / TK4- baseline)
DEFAULT_SYSTEM_MACLIBS = ["SYS1.MACLIB", "SYS1.AMODGEN"]


@dataclass
class ResolvedDataset:
    """A fully qualified dataset with all attributes."""
    dsn: str           # e.g. "IBMUSER.HTTPD.V3R3M1.NCALIB"
    key: str           # e.g. "ncalib"
    suffix: str        # e.g. "NCALIB"
    dsorg: str
    recfm: str
    lrecl: int
    blksize: int
    space: list
    unit: str
    volume: str | None
    local_dir: str | None


class DatasetResolver:
    """Compute dataset names from config.

    Results are cached internally — methods can be called
    multiple times without recomputation.
    """

    def __init__(self, config: MbtConfig):
        self.config = config
        self._build_cache: dict[str, ResolvedDataset] | None = None
        self._install_cache: dict[str, ResolvedDataset] | None = None

    def build_datasets(self) -> dict[str, ResolvedDataset]:
        """Compute build dataset names.

        Pattern: {HLQ}.{PROJECT}.{VRM}.{SUFFIX}
        CI mode: {HLQ}.{PROJECT}.B{BUILD_ID}.{SUFFIX}

        Results are cached after first call.
        """
        if self._build_cache is None:
            self._build_cache = self._compute_build_datasets()
        return self._build_cache

    def _compute_build_datasets(self) -> dict[str, ResolvedDataset]:
        """Internal: compute without caching."""
        config = self.config
        project = config.project
        hlq = config.hlq
        proj_name = project.name.upper()

        if config.is_ci:
            qualifier = f"B{config.build_id}"
        else:
            qualifier = project.vrm

        result = {}
        for key, ds in project.build_datasets.items():
            dsn = f"{hlq}.{proj_name}.{qualifier}.{ds.suffix}"
            result[key] = ResolvedDataset(
                dsn=dsn,
                key=key,
                suffix=ds.suffix,
                dsorg=ds.dsorg,
                recfm=ds.recfm,
                lrecl=ds.lrecl,
                blksize=ds.blksize,
                space=ds.space,
                unit=ds.unit,
                volume=ds.volume,
                local_dir=ds.local_dir,
            )
        return result

    def dependency_datasets(self,
                            lockfile_deps: dict[str, str],
                            package_cache: dict
                            ) -> dict[str, list[ResolvedDataset]]:
        """Compute dependency dataset names.

        Pattern: {DEPS_HLQ}.{DEP_NAME}.{DEP_VRM}.{SUFFIX}

        Args:
            lockfile_deps: {"mvslovers/crent370": "1.0.0", ...}
            package_cache: cached package.toml data per dep

        Returns:
            {"mvslovers/crent370": [ResolvedDataset, ...], ...}
        """
        result = {}
        deps_hlq = self.config.deps_hlq

        for dep_key, dep_version in lockfile_deps.items():
            dep_vrm = to_vrm(dep_version)

            datasets = []
            pkg = package_cache.get(dep_key, {})
            pkg_name = pkg.get("package", {}).get("name") or dep_key.split("/")[-1]
            dep_name = pkg_name.upper()[:8]
            provides = pkg.get("mvs", {}).get("provides", {}).get("datasets", {})
            for ds_key, ds_data in provides.items():
                suffix = ds_data["suffix"]
                if deps_hlq:
                    dsn = f"{deps_hlq}.{dep_name}.{dep_vrm}.{suffix}"
                else:
                    dsn = f"{dep_name}.{dep_vrm}.{suffix}"
                datasets.append(ResolvedDataset(
                    dsn=dsn,
                    key=ds_key,
                    suffix=suffix,
                    dsorg=ds_data.get("dsorg", "PO"),
                    recfm=ds_data.get("recfm", "FB"),
                    lrecl=int(ds_data.get("lrecl", 80)),
                    blksize=int(ds_data.get("blksize", 3120)),
                    space=list(ds_data.get("space", ["TRK", 10, 5, 10])),
                    unit=ds_data.get("unit", "SYSDA"),
                    volume=ds_data.get("volume"),
                    local_dir=None,
                ))
            result[dep_key] = datasets
        return result

    def install_datasets(self) -> dict[str, ResolvedDataset]:
        """Compute install dataset names.

        Pattern depends on install_naming:
          "fixed": {HLQ}.{name}
          "vrm":   {HLQ}.{PROJECT}.{VRM}.{SUFFIX}

        Results are cached after first call.
        """
        if self._install_cache is None:
            self._install_cache = self._compute_install_datasets()
        return self._install_cache

    def _compute_install_datasets(self) -> dict[str, ResolvedDataset]:
        """Internal: compute without caching."""
        config = self.config
        project = config.project
        hlq = config.hlq
        proj_name = project.name.upper()

        if not project.install_naming:
            return {}

        result = {}
        for key, inst_ds in project.install_datasets.items():
            build_ds = project.build_datasets.get(key)
            if build_ds is None:
                continue

            if project.install_naming == "fixed":
                dsn = f"{hlq}.{inst_ds.name}"
            else:  # vrm
                dsn = f"{hlq}.{proj_name}.{project.vrm}.{build_ds.suffix}"

            result[key] = ResolvedDataset(
                dsn=dsn,
                key=key,
                suffix=build_ds.suffix,
                dsorg=build_ds.dsorg,
                recfm=build_ds.recfm,
                lrecl=build_ds.lrecl,
                blksize=build_ds.blksize,
                space=build_ds.space,
                unit=build_ds.unit,
                volume=build_ds.volume,
                local_dir=None,
            )
        return result

    def syslib_maclibs(self,
                       lockfile_deps: dict[str, str],
                       package_cache: dict
                       ) -> list[str]:
        """Build SYSLIB MACLIB concatenation list.

        Order (fixed, per spec section 8.3):
        1. Project's own MACLIB (if defined)
        2. Dependency MACLIBs (declaration order)
        3. SYS1.MACLIB and SYS1.AMODGEN (always present)
        4. Additional system MACLIBs from [system] maclibs in project.toml

        Returns:
            List of fully qualified dataset names
        """
        result = []

        # 1. Project's own MACLIB
        build_ds = self.build_datasets()
        if "maclib" in build_ds:
            result.append(build_ds["maclib"].dsn)

        # 2. Dependency MACLIBs (declaration order from [dependencies])
        dep_datasets = self.dependency_datasets(lockfile_deps, package_cache)
        for dep_key in self.config.project.dependencies:
            if dep_key in dep_datasets:
                for ds in dep_datasets[dep_key]:
                    if ds.suffix == "MACLIB":
                        result.append(ds.dsn)

        # 3. System MACLIBs: hardcoded defaults + project extras
        result.extend(DEFAULT_SYSTEM_MACLIBS)
        result.extend(self.config.project.system_maclibs)
        return result

    def syslib_ncalibs(self,
                       lockfile_deps: dict[str, str],
                       package_cache: dict
                       ) -> list[str]:
        """Build SYSLIB DD dataset list for IEWL.

        Contains project NCALIB and autocall-compatible dependency
        NCaLIBs only. Non-autocall deps go to ncalib_dd_dsns() instead.

        Order: project NCALIB first, then autocall deps in
        declaration order.
        """
        result = []

        # 1. Project's NCALIB
        build_ds = self.build_datasets()
        if "ncalib" in build_ds:
            result.append(build_ds["ncalib"].dsn)

        # 2. Autocall dependency NCaLIBs (declaration order)
        dep_datasets = self.dependency_datasets(lockfile_deps, package_cache)
        for dep_key in self.config.project.dependencies:
            if dep_key in dep_datasets:
                pkg = package_cache.get(dep_key, {})
                if not pkg.get("link", {}).get("autocall", True):
                    continue  # non-autocall goes to NCALIB DD only
                for ds in dep_datasets[dep_key]:
                    if ds.suffix == "NCALIB":
                        result.append(ds.dsn)

        return result

    def ncalib_dd_dsns(self,
                       lockfile_deps: dict[str, str],
                       package_cache: dict
                       ) -> list[str]:
        """Build NCALIB DD dataset list for IEWL.

        Contains project NCALIB (for explicit own-module INCLUDEs)
        and non-autocall dependency NCaLIBs (for dep_includes INCLUDEs).

        Order: project NCALIB first, then non-autocall deps in
        declaration order.
        """
        result = []

        # 1. Project's NCALIB
        build_ds = self.build_datasets()
        if "ncalib" in build_ds:
            result.append(build_ds["ncalib"].dsn)

        # 2. Non-autocall dependency NCaLIBs (declaration order)
        dep_datasets = self.dependency_datasets(lockfile_deps, package_cache)
        for dep_key in self.config.project.dependencies:
            if dep_key in dep_datasets:
                pkg = package_cache.get(dep_key, {})
                if pkg.get("link", {}).get("autocall", True):
                    continue  # autocall goes to SYSLIB only
                for ds in dep_datasets[dep_key]:
                    if ds.suffix == "NCALIB":
                        result.append(ds.dsn)

        return result
