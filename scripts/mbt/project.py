"""project.toml parser and validator.

Loads the single project configuration file and provides
typed access to all sections. Validates required fields,
project types, dataset definitions, and space arrays.
"""

import tomllib
from dataclasses import dataclass, field
from pathlib import Path

from .version import Version


class ProjectError(Exception):
    """Raised when project.toml is invalid."""
    pass


VALID_TYPES = {"runtime", "library", "module", "application"}
LINK_TYPES = {"module", "application"}


@dataclass
class DatasetDef:
    """Parsed dataset definition from [mvs.build.datasets.*]."""
    key: str           # TOML key, e.g. "ncalib"
    suffix: str        # e.g. "NCALIB"
    dsorg: str         # "PO" or "PS"
    recfm: str         # "FB", "VB", "U"
    lrecl: int
    blksize: int
    space: list        # ["TRK", pri, sec, dir] or ["TRK", pri, sec]
    unit: str = "SYSDA"
    volume: str | None = None
    local_dir: str | None = None  # local dir to upload during bootstrap

    def space_unit(self) -> str:
        return self.space[0]

    def space_primary(self) -> int:
        return self.space[1]

    def space_secondary(self) -> int:
        return self.space[2]

    def space_dirblks(self) -> int | None:
        """Returns dirblks for PO, None for PS."""
        return self.space[3] if len(self.space) == 4 else None


@dataclass
class LinkModule:
    """Parsed [link.module] entry."""
    name: str          # load module name, e.g. "HTTPD"
    entry: str         # entry point (default: @@CRT0)
    options: list[str] # IEWL options, e.g. ["LIST", "XREF", "LET"]
    include: list[str] # NCALIB members to include (default: ["@@CRT1", name])


@dataclass
class InstallDataset:
    """Parsed [mvs.install.datasets.*] entry."""
    key: str           # matches build dataset key
    name: str          # e.g. "HTTPD.LOAD"


@dataclass
class ProjectConfig:
    """Complete parsed project configuration."""

    # [project]
    name: str
    version: str
    type: str          # "runtime", "library", "module", "application"

    # [build]
    cflags: list[str] = field(default_factory=list)
    c_dirs: list[str] = field(default_factory=lambda: ["src/"])
    asm_dirs: list[str] = field(default_factory=list)

    # [dependencies]
    dependencies: dict[str, str] = field(default_factory=dict)

    # [mvs.asm]
    max_rc: int = 4

    # [mvs.build.datasets.*]
    build_datasets: dict[str, DatasetDef] = field(default_factory=dict)

    # [mvs.install]
    install_naming: str | None = None  # "fixed" or "vrm"
    install_datasets: dict[str, InstallDataset] = field(default_factory=dict)

    # [link]
    link_modules: list[LinkModule] = field(default_factory=list)

    # [artifacts]
    artifact_headers: bool = False
    artifact_mvs: bool = False
    artifact_bundle: bool = False
    artifact_mvs_datasets: list[str] = field(default_factory=list)

    # [release]
    release_github: str | None = None   # "owner/repo"
    release_version_files: list[str] = field(default_factory=list)

    @classmethod
    def load(cls, path: str | Path = "project.toml") -> "ProjectConfig":
        """Load and validate project.toml.

        Args:
            path: Path to project.toml

        Returns:
            Validated ProjectConfig instance

        Raises:
            ProjectError: If file is missing or invalid
            FileNotFoundError: If file does not exist
        """
        path = Path(path)
        if not path.exists():
            raise FileNotFoundError(f"project.toml not found: {path}")
        try:
            with open(path, "rb") as f:
                data = tomllib.load(f)
        except tomllib.TOMLDecodeError as e:
            raise ProjectError(f"TOML parse error in {path}: {e}")
        config = cls._parse(data)
        config._validate()
        return config

    @classmethod
    def _parse(cls, data: dict) -> "ProjectConfig":
        """Parse raw TOML dict into ProjectConfig.

        Handles all section parsing, default values, and
        type conversion. Does NOT validate — call _validate()
        after parsing.
        """
        proj = data.get("project", {})
        build = data.get("build", {})
        sources = build.get("sources", {})
        deps = data.get("dependencies", {})
        mvs = data.get("mvs", {})
        asm_cfg = mvs.get("asm", {})
        mvs_build = mvs.get("build", {})
        mvs_datasets = mvs_build.get("datasets", {})
        mvs_install = mvs.get("install", {})
        link = data.get("link", {})
        artifacts = data.get("artifacts", {})
        release = data.get("release", {})

        # Build datasets
        build_datasets = {}
        for key, ds_data in mvs_datasets.items():
            build_datasets[key] = DatasetDef(
                key=key,
                suffix=ds_data["suffix"],
                dsorg=ds_data["dsorg"],
                recfm=ds_data["recfm"],
                lrecl=int(ds_data["lrecl"]),
                blksize=int(ds_data["blksize"]),
                space=list(ds_data["space"]),
                unit=ds_data.get("unit", "SYSDA"),
                volume=ds_data.get("volume"),
                local_dir=ds_data.get("local_dir"),
            )

        # Install datasets
        install_ds_data = mvs_install.get("datasets", {})
        install_datasets = {}
        for key, inst_data in install_ds_data.items():
            install_datasets[key] = InstallDataset(
                key=key,
                name=inst_data["name"],
            )

        # Link module: [link.module] is a single table.
        # Also accept [[link.module]] (array-of-tables) for compat.
        link_modules = []
        raw_modules = link.get("module", [])
        if isinstance(raw_modules, dict):
            raw_modules = [raw_modules]
        for mod in raw_modules:
            mod_name = mod["name"]
            link_modules.append(LinkModule(
                name=mod_name,
                entry=mod.get("entry", "@@CRT0"),
                options=list(mod.get("options", [])),
                include=list(mod.get("include", ["@@CRT1", mod_name])),
            ))

        # Source directories — partial override is allowed
        if sources:
            c_dirs = list(sources.get("c_dirs", ["src/"]))
            asm_dirs = list(sources.get("asm_dirs", []))
        else:
            c_dirs = ["src/"]
            asm_dirs = []

        return cls(
            name=proj.get("name", ""),
            version=proj.get("version", ""),
            type=proj.get("type", ""),
            cflags=list(build.get("cflags", [])),
            c_dirs=c_dirs,
            asm_dirs=asm_dirs,
            dependencies=dict(deps),
            max_rc=int(asm_cfg.get("max_rc", 4)),
            build_datasets=build_datasets,
            install_naming=mvs_install.get("naming"),
            install_datasets=install_datasets,
            link_modules=link_modules,
            artifact_headers=bool(artifacts.get("headers", False)),
            artifact_mvs=bool(artifacts.get("mvs", False)),
            artifact_bundle=bool(artifacts.get("package_bundle", False)),
            artifact_mvs_datasets=list(artifacts.get("mvs_datasets", [])),
            release_github=release.get("github"),
            release_version_files=list(release.get("version_files", [])),
        )

    def _validate(self) -> None:
        """Validate the parsed config.

        Checks:
        - Required fields present
        - project.type is valid
        - Dataset space arrays have correct length
        - PO datasets have 4-element space (with dirblks)
        - PS datasets have 3-element space
        - link section only for application/module types
        - install section references existing build datasets

        Raises:
            ProjectError: With descriptive message
        """
        if not self.name:
            raise ProjectError("Missing required field: [project] name")
        if not self.version:
            raise ProjectError("Missing required field: [project] version")
        if not self.type:
            raise ProjectError("Missing required field: [project] type")

        if self.type not in VALID_TYPES:
            raise ProjectError(
                f"Invalid project type '{self.type}'. "
                f"Must be one of: {', '.join(sorted(VALID_TYPES))}"
            )

        # Validate version is parseable semver
        try:
            Version.parse(self.version)
        except ValueError as e:
            raise ProjectError(f"Invalid version in [project] version: {e}")

        # Validate dataset space arrays
        for key, ds in self.build_datasets.items():
            if ds.dsorg == "PO":
                if len(ds.space) != 4:
                    raise ProjectError(
                        f"Dataset '{key}' (dsorg=PO) space array must have "
                        f"4 elements [unit, primary, secondary, dirblks], "
                        f"got {len(ds.space)}"
                    )
            elif ds.dsorg == "PS":
                if len(ds.space) != 3:
                    raise ProjectError(
                        f"Dataset '{key}' (dsorg=PS) space array must have "
                        f"3 elements [unit, primary, secondary], "
                        f"got {len(ds.space)}"
                    )

        # Link section only for application/module types
        if self.link_modules and self.type not in LINK_TYPES:
            raise ProjectError(
                f"[link] section is only allowed for project types: "
                f"{', '.join(sorted(LINK_TYPES))}. "
                f"Current type: '{self.type}'"
            )

        # Install section must reference existing build datasets
        for key in self.install_datasets:
            if key not in self.build_datasets:
                raise ProjectError(
                    f"Install dataset '{key}' references non-existent "
                    f"build dataset. "
                    f"Available build datasets: "
                    f"{', '.join(self.build_datasets.keys()) or '(none)'}"
                )

    @property
    def parsed_version(self) -> Version:
        """Return parsed Version object."""
        return Version.parse(self.version)

    @property
    def vrm(self) -> str:
        """Return MVS VRM string for this version."""
        return self.parsed_version.to_vrm()

    def datasets_with_local_dir(self) -> list[DatasetDef]:
        """Return datasets that have local_dir set (need upload)."""
        return [ds for ds in self.build_datasets.values()
                if ds.local_dir is not None]
