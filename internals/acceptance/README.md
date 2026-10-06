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

## `deps23.py` — dependency staging

```
python3 internals/acceptance/deps23.py <mbt3-binary> <workdir> PROJ ...
```

In checkouts `diff23.py` left behind: `make deps ARGS=--update`, then
`mbt deps --update`, each from an empty `.mbt/deps`, and compares the staged
tree file by file and `mbt.lock`. Without `--update` the two differ on
purpose: mbt 3 never moves a pin by itself (a drifted SHA is an error), where
mbt 2 re-pinned a republished prerelease with a warning.

## `pkg23.py` — release artifacts

```
python3 internals/acceptance/pkg23.py <mbt3-binary> <workdir> PROJ ...
```

`make package` against `mbt package`: the load XMIT byte for byte; the lib
tarball and the SMP installation package (dist zip and tar.gz) member by
member. Ignored: AppleDouble `._` members from a macOS tar (#161) and
`httpd-webroot.img`, which is not reproducible by construction.

httpd's Makefile adds a step of its own (`make webroot`: a UFS image built
with a pinned `ufsd-utils`, shipped as `[distribution] extra`). mbt 3 has no
tasks yet, so the script runs that step before `mbt package` and says so.

## `deploy23.py` — deploy and test-mvs, against a stand-in for mvsMF

```
python3 internals/acceptance/deploy23.py <mbt3-binary> <built checkout> [spool-file] [--test]
```

`--test` compares `make test-mvs` with `mbt test --mvs` instead of the deploy.
The spool file stands in for every spool file the runner job returns; a real
one (a saved MBTTEST spool) exercises the verdict parsing on real JES lines.

`make deploy` and `mbt deploy` each talk to `stub_mvsmf.py` on 127.0.0.1,
which records every request and answers like a healthy server; no MVS is
touched, and a checkout with a `.env` is refused. Compared: the request
sequence (uploaded bytes and JSON bodies included), the submitted JCL and
the console lines. A spool file with a JCL error exercises the failure path.
What this cannot show is how the real mvsMF answers; that needs one deploy
on a real system.

## `baseline.py` — the recorded manifest

```
python3 internals/acceptance/baseline.py internals/baseline/v2-manifest.tsv <workdir> [--build CMD] [--only PROJ ...]
```

Clones each project at the manifest's commit, builds it with the pinned clock
and compares against the manifest. It answers "does this still reproduce what
was recorded" — useful as a control, but not as the mbt 3 gate, for the reason
above.
