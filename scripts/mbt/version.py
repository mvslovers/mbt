"""Semantic versioning and MVS VRM format conversion.

This module handles parsing of semver version strings and
converting them to the MVS VRM (Version/Release/Modification)
naming format used in dataset qualifiers.
"""

import re
from dataclasses import dataclass

# Semver pattern: MAJOR.MINOR.PATCH[-prerelease]
# Prerelease: "dev" or "rc" followed by digits
_SEMVER_RE = re.compile(
    r"^(\d+)\.(\d+)\.(\d+)(?:-(dev|rc\d+))?$"
)


@dataclass(frozen=True)
class Version:
    """Parsed semantic version."""
    major: int
    minor: int
    patch: int
    pre: str | None = None  # None, "dev", "rc1", "rc2", ...

    @classmethod
    def parse(cls, version_str: str) -> "Version":
        """Parse a semver string.

        Args:
            version_str: Version string like "1.0.0" or "3.3.1-dev"

        Returns:
            Version instance

        Raises:
            ValueError: If version_str is not valid semver
        """
        m = _SEMVER_RE.match(version_str.strip())
        if not m:
            raise ValueError(
                f"Invalid semver: {version_str!r}. "
                f"Expected MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-dev "
                f"or MAJOR.MINOR.PATCH-rcN"
            )
        return cls(
            major=int(m.group(1)),
            minor=int(m.group(2)),
            patch=int(m.group(3)),
            pre=m.group(4),
        )

    def to_vrm(self) -> str:
        """Convert to MVS VRM format.

        Returns:
            VRM string, e.g. "V1R0M0", "V3R3M1", "V1R0M0D", "V3R3M1R1"

        Mapping:
            1.0.0     -> V1R0M0
            3.3.1     -> V3R3M1
            1.0.0-dev -> V1R0M0D
            3.3.1-rc1 -> V3R3M1R1
        """
        vrm = f"V{self.major}R{self.minor}M{self.patch}"
        if self.pre == "dev":
            vrm += "D"
        elif self.pre is not None and self.pre.startswith("rc"):
            n = self.pre[2:]
            vrm += f"R{n}"
        return vrm

    def __str__(self) -> str:
        """Return original semver string."""
        base = f"{self.major}.{self.minor}.{self.patch}"
        if self.pre is not None:
            return f"{base}-{self.pre}"
        return base

    def _as_tuple(self) -> tuple:
        """For comparison, including prerelease rank.

        Prerelease ranking (per semver):
            dev     -> -2   (lowest)
            rc<N>   -> -1   (higher than dev, with rc number as tiebreaker)
            release ->  0   (highest)

        This ensures: 1.0.0-dev < 1.0.0-rc1 < 1.0.0
        """
        if self.pre is None:
            return (self.major, self.minor, self.patch, 0, 0)
        elif self.pre == "dev":
            return (self.major, self.minor, self.patch, -2, 0)
        else:  # rc<N>
            n = int(self.pre[2:])
            return (self.major, self.minor, self.patch, -1, n)

    def __lt__(self, other: "Version") -> bool:
        return self._as_tuple() < other._as_tuple()

    def __le__(self, other: "Version") -> bool:
        return self._as_tuple() <= other._as_tuple()

    def __gt__(self, other: "Version") -> bool:
        return self._as_tuple() > other._as_tuple()

    def __ge__(self, other: "Version") -> bool:
        return self._as_tuple() >= other._as_tuple()


def satisfies(version_str: str, constraint: str) -> bool:
    """Check if a version satisfies a constraint expression.

    Supported operators: >=, <, =
    Multiple constraints joined by comma (AND logic).

    Args:
        version_str: Version to check, e.g. "1.2.0"
        constraint: Constraint expression, e.g. ">=1.0.0,<2.0.0"

    Returns:
        True if version satisfies all constraints

    Examples:
        satisfies("1.5.0", ">=1.0.0")        -> True
        satisfies("2.0.0", ">=1.0.0,<2.0.0") -> False
        satisfies("1.0.0", "=1.0.0")         -> True
        satisfies("1.0.0-dev", ">=1.0.0")    -> False (dev < release)
    """
    version = Version.parse(version_str)
    for part in constraint.split(","):
        part = part.strip()
        if part.startswith(">="):
            req = Version.parse(part[2:])
            if not (version >= req):
                return False
        elif part.startswith("<"):
            req = Version.parse(part[1:])
            if not (version < req):
                return False
        elif part.startswith("="):
            req = Version.parse(part[1:])
            if version != req:
                return False
        else:
            raise ValueError(f"Unknown constraint operator in: {part!r}")
    return True


def to_vrm(version_str: str) -> str:
    """Convenience: parse and convert to VRM in one call."""
    return Version.parse(version_str).to_vrm()
