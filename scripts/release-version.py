#!/usr/bin/env python3
"""Select a stable patch version, reusing an existing tag for the same commit."""
import re
import subprocess


def select_version(tags, commit):
    stable = [(tuple(map(int, match.groups())), tag, sha)
              for tag, sha in tags
              if (match := re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", tag))]
    matching = [entry for entry in stable if entry[2] == commit]
    if matching:
        return max(matching)[1], False
    major, minor, patch = max((entry[0] for entry in stable), default=(0, 1, -1))
    return f"v{major}.{minor}.{patch + 1}", True


if __name__ == "__main__":
    def git(*args):
        return subprocess.check_output(["git", *args], text=True).strip()

    tags = [(tag, git("rev-parse", f"{tag}^{{commit}}"))
            for tag in git("tag", "--list", "v*").splitlines()]
    version, create = select_version(tags, git("rev-parse", "HEAD"))
    print(f"tag={version}")
    print(f"create={'true' if create else 'false'}")
