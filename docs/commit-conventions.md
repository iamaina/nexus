# Commit Conventions

Every commit merged to `master` must follow the Conventional Commits format. This is not optional — the CI pipeline reads the commit message to determine the next version tag. A non-conforming message fails the build.

---

## Format

```
type(scope): subject
```

- **type** — what kind of change (see table below)
- **scope** — optional, what part of the codebase changed (`context`, `query`, `layout`, `setup`, etc.)
- **subject** — short description in the imperative mood ("add X", not "added X" or "adds X")

**Examples:**

```
feat(context): add HTTP-based context sources for Prometheus API
fix(query): handle empty result set when no sources are ingested
docs: add OCR setup instructions to getting-started
chore(deps): upgrade pgx to v5.7
breaking: replace block-count chunking with token-aware chunking
```

---

## Types and version impact

| Type | When to use | Version bump |
|---|---|---|
| `breaking` / `major` | Incompatible change — removes a flag, changes DB schema destructively, changes config format | `v1.0.0` → `v2.0.0` |
| `feat` | New user-visible feature | `v1.0.0` → `v1.1.0` |
| `fix` | Bug fix | `v1.0.0` → `v1.0.1` |
| `perf` | Performance improvement, no behaviour change | `v1.0.0` → `v1.0.1` |
| `refactor` | Code restructure, no behaviour change | `v1.0.0` → `v1.0.1` |
| `docs` | Documentation only | `v1.0.0` → `v1.0.1` |
| `chore` | Dependency updates, tooling, CI changes | `v1.0.0` → `v1.0.1` |
| `test` | Adding or fixing tests | `v1.0.0` → `v1.0.1` |

If your change spans multiple types, pick the **highest impact** type. A PR that adds a feature and fixes a bug is `feat:`.

---

## Branch workflow

Work happens on the current `stable/vX.Y.Z` branch, which CI creates automatically after each release tag. For isolated features:

```bash
git checkout stable/v0.4.0
git checkout -b feat/my-feature
# ... commits ...
git push origin feat/my-feature
# open PR into stable/v0.4.0
```

When the milestone is complete, the stable branch is squashed and merged to master.

---

## Before opening a PR: squash your commits

Your working branch will accumulate many commits as you iterate (`wip`, `fix typo`, `try again`, etc.). Before opening a PR **into master**, squash them all into **one well-formed conventional commit**.

### Step-by-step

**1. See what you have:**
```bash
git log master..HEAD --oneline
```

Example output:
```
f3a1b2c fix typo
e9d4a1f try different approach
c7b8d3e wip: context sources
a1f2e3d start live context work
```

**2. Start an interactive rebase:**
```bash
git rebase -i master
```

Your editor opens with a list of your commits, oldest at the top:
```
pick a1f2e3d start live context work
pick c7b8d3e wip: context sources
pick e9d4a1f try different approach
pick f3a1b2c fix typo
```

**3. Squash everything into the first commit.**
Change every `pick` except the first to `s` (squash):
```
pick a1f2e3d start live context work
s c7b8d3e wip: context sources
s e9d4a1f try different approach
s f3a1b2c fix typo
```

Save and close the editor.

**4. Write the final commit message.**
A second editor window opens. Delete all the old messages and write one clean conventional commit:
```
feat(context): add live context sources pipeline

Register shell commands whose output is injected into every query
prompt alongside static document chunks.

- nexus context add|list|rm|run — CRUD for registered sources
- RunAll executes sources concurrently with 5s timeout
- SummarizeWithLive injects live output into the LLM prompt
- --no-live flag on query to skip live sources
```

Save and close. The rebase completes.

**5. Verify:**
```bash
git log master..HEAD --oneline
# Should show exactly one commit with your conventional commit message
```

**6. Force-push your branch** (history was rewritten):
```bash
git push --force origin release/vX.Y.Z
```

---

## What happens when you merge

When the PR merges to `master`, the CI pipeline reads the squashed commit message and automatically creates a version tag:

```
feat(context): add live context sources pipeline
  → current tag v0.2.0
  → new tag     v0.3.0
```

No manual tagging needed. The tag appears in the repository within seconds of the merge.

---

## Common mistakes

**Merge commit as the PR commit**
If your PR is merged with a generic "Merge pull request #N" message the CI will fail. Use squash merge, or squash locally before merging.

**Multiple commits reach master**
Only the most recent commit is read by the CI. Make sure you squash to one before merging.

**Wrong type**
If you pick `fix:` for a new feature, the version bump will be wrong (patch instead of minor). Take a moment to pick the right type — it directly controls the version number.

**`wip:` or non-standard type**
The CI does not recognise `wip`, `update`, `change`, or any other free-form type. It will fail and point you back here.
