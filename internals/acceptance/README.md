# Acceptance of mbt 3 against mbt 2

mbt 3 is accepted when, for every project, it produces what mbt 2 produces
(`internals/mbt-3-design.md`, section 16). Two tools measure that.

## `diff23.py` — the gate

```
python3 internals/acceptance/diff23.py <mbt3-binary> <workdir> PROJ[@COMMIT] ...
```

For each project, in one checkout: `make deps` (with `mbt.lock` restored
afterwards, so the tree stays clean), mbt 2 builds (`make all`, `make test`),
`build/` is hashed and wiped, mbt 3 builds (`mbt build --all --tests`) on the
same staged dependencies, and every object deck, archive and load module is
compared. The clock is pinned as for the baseline.

Both builds run side by side because a stored manifest cannot be reproduced
once a dependency is a moving prerelease: `1.0.0-dev` tarballs are republished
on every push, and the old one is gone (see `../v2-baseline.md`, "Found along
the way"). Comparing against a fresh mbt 2 build on the same inputs removes
that variable.

A **negative control** belongs to every change of this tool: build mbt 3 with
one deliberate difference (say `-O2` instead of `-O1`) and confirm the
comparison fails.

## `baseline.py` — the recorded manifest

```
python3 internals/acceptance/baseline.py internals/baseline/v2-manifest.tsv <workdir> [--build CMD] [--only PROJ ...]
```

Clones each project at the manifest's commit, builds it with the pinned clock
and compares against the manifest. It answers "does this still reproduce what
was recorded" — useful as a control, but not as the mbt 3 gate, for the reason
above.
