import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("release_version", Path(__file__).with_name("release-version.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ReleaseVersionTests(unittest.TestCase):
    def test_increments_highest_stable_version_numerically(self):
        self.assertEqual(module.select_version([("v0.1.9", "a"), ("v0.1.10", "b"), ("v1.0.0-rc1", "c")], "new"), ("v0.1.11", True))

    def test_retry_reuses_commit_tag_even_after_newer_releases(self):
        self.assertEqual(module.select_version([("v0.1.1", "same"), ("v0.1.2", "newer")], "same"), ("v0.1.1", False))

    def test_empty_repository_starts_at_first_patch(self):
        self.assertEqual(module.select_version([], "new"), ("v0.1.0", True))


if __name__ == "__main__":
    unittest.main()
