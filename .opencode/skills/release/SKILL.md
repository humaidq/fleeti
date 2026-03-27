---
name: release
description: Create semver patch, minor, or major releases for Fleeti by updating the app version, maintaining a root changelog, validating the release diff, and creating an annotated git tag.
license: Apache-2.0
compatibility: opencode
metadata:
  audience: maintainers
  project: fleeti
---

## What I do

I create Fleeti releases in this repository with a consistent semver workflow.

- Accept release intents for `patch`, `minor`, or `major` updates.
- Treat `src/default.nix` as the canonical application version.
- Append a new dated section to `CHANGELOG.md`, creating the file if it does not exist yet.
- Build release notes from both git history and the actual code diff since the previous release tag, because commit messages alone are not enough.
- Create an annotated git tag named `vX.Y.Z`.

## When to use me

Use this skill when the user asks to create a release, cut a patch release, bump minor or major, update the changelog for a release, or tag a new version.

Do not use this skill for ordinary code changes that are not release-related.

## Release rules

- Follow semver exactly.
- Fleeti currently stores the canonical version in `src/default.nix` with a `v` prefix, for example `v0.1.1`.
- Git tags also use the `v` prefix and must match the package version exactly.
- `CHANGELOG.md` lives at the repository root and is ordered newest-first.
- Every release section header must use this format: `## [vX.Y.Z] - YYYY-MM-DD`.
- Release notes must be derived from both:
  - `git log` since the previous semver tag
  - `git diff` since the previous semver tag
- If there is no previous tag, treat the current version in `src/default.nix` as the baseline version and inspect the full available history from `HEAD`.
- Never invent changes that are not supported by commits or diffs.
- Prefer concise, user-facing bullets that describe behavior, features, fixes, or operational changes.
- Ignore unrelated version fields such as docs package metadata unless the user explicitly asks to release those too.

## Files I touch

- `CHANGELOG.md`
- `src/default.nix`

## How to perform a release

When the user asks for a `patch`, `minor`, or `major` release, do this in order:

1. Inspect repository state.
   - Check `git status --short`.
   - Read the current version from `src/default.nix`.
   - Find the latest semver tag with a `v` prefix, if one exists.

2. Determine the next version.
   - `patch`: increment `Z` in `X.Y.Z`
   - `minor`: increment `Y` and reset `Z` to `0`
   - `major`: increment `X` and reset `Y` and `Z` to `0`

3. Collect release evidence.
   - Review commit history since the latest tag. Use enough history to understand the release, not just the latest commit.
   - Review the code diff since the latest tag.
   - If no tag exists, review the full available history and current tree to produce the initial generated release notes.

4. Write release notes.
   - Append a new section near the top of `CHANGELOG.md`, directly under the title and introductory sentence if present.
   - If `CHANGELOG.md` does not exist, create it with a short introduction and the new release section.
   - Use a short list of bullets.
   - Describe the release in terms of meaningful outcomes, not a raw dump of commit subjects.
   - Mention real subsystems when supported by the diff, such as profiles, fleets, builds, releases, devices, rollouts, authentication, or Nix image/update plumbing.

5. Update versioned files.
   - Set `src/default.nix` to the new version.

6. Validate the release content.
   - Review the diff for `CHANGELOG.md` and `src/default.nix`.
   - Ensure the changelog date matches the current local date.
   - Ensure the new changelog header and git tag version match exactly.
   - Prefer running `go test ./...` from `src/` and `nix build .#fleeti` from the repo root before finalizing when the environment permits.

7. Commit and tag the release when the user asked to create the release.
   - Create a release commit, typically `release: vX.Y.Z`.
   - Create an annotated tag: `git tag -a vX.Y.Z -m "Fleeti vX.Y.Z"`.
   - Do not push unless the user explicitly asks.

## Changelog format

Use this structure:

```md
# Changelog

All notable Fleeti releases are tracked here.

## [v0.1.2] - 2026-03-18

- Improve ...
- Add ...
- Fix ...
```

## Notes on judgment

- If the repo has unrelated dirty files, avoid touching them.
- If a required release file already has user edits, read carefully and preserve those changes while completing the release.
- If no meaningful changes exist since the latest tag, say so clearly before creating a redundant release.
- If the user asks only to prepare release files but not finalize the release, update files and stop before commit or tag.
