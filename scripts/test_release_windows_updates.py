import unittest

from release_windows_updates import next_version, release_tag


class ReleaseTagTests(unittest.TestCase):
    def test_version_progression(self):
        self.assertEqual(release_tag('1.1.28+159'), 'tag1.1.28')
        self.assertEqual(next_version('1.1.28+159'), '1.1.29+160')
        self.assertEqual(next_version('1.1.29+160'), '1.1.30+161')
        self.assertEqual(release_tag('1.1.30+161'), 'tag1.1.30')

    def test_invalid_versions_rejected(self):
        for value in ['1.1.28', 'tag1.1.28', '1.1.029+160', '1.1.28+0']:
            with self.assertRaises(ValueError):
                next_version(value)
