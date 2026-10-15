# Publishing to GitHub on a review branch

Goal: put hloop on a new branch of your existing repo, look it over, then merge to `main` when you're happy.

```sh
git clone https://github.com/<user>/<repo>.git && cd <repo>
git checkout -b harness-agnostic-cli

# replace the old contents on this branch (main keeps them in history)
git rm -rq . && git clean -fdxq -e .git
unzip ~/Downloads/hloop-0.2.0.zip -d .      # extract at the repo root, so go.mod sits next to README.md
rm -rf dist                                  # binaries are built by CI, not committed

scripts/set-repo.sh <user>/<repo>            # point install scripts and docs at your repo
git add -A
git commit -m "Rewrite as hloop: harness-agnostic CLI"
git push -u origin harness-agnostic-cli
```

Then:

1. **Open a pull request** into `main`. CI (`.github/workflows/ci.yml`) runs vet and tests on Ubuntu, macOS and Windows. This is the first real test of the macOS and Windows code, so read its results.
2. **Try a pre-release from the branch** (a release is what the one-line installer downloads; a branch alone doesn't produce one):
   ```sh
   git tag v0.2.0-rc1 && git push origin v0.2.0-rc1
   ```
   The `release` workflow runs the tests, builds all six platforms, and publishes a GitHub Release. Install it with the pinned-version command in [INSTALL.md](INSTALL.md). Edit the release on GitHub and tick "pre-release" if you don't want it to become "latest".
3. **When satisfied**, merge the PR and tag `v0.2.0`. From then on, the unpinned one-liner installs the latest release.

Notes:
- If you'd rather keep the old antigravity-harness untouched, create a new repo (`harness-loop`) instead and push there; the commands are the same.
- Nothing in the project needs secrets. Bot tokens and ntfy topics are stored on each user's machine, not in the repo.
- The workflows were written but have not been run on GitHub yet; if one fails, the log will say why.
