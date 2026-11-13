import base64
import json
import tempfile
import unittest
from pathlib import Path

from bump_version import next_version
from license_build import load_license_build, license_linker_flags, SERVER, customer_distribution_id


class LicenseBuildTests(unittest.TestCase):
    def config(self):
        public = base64.urlsafe_b64encode(bytes(range(32))).decode().rstrip('=')
        return dict(protocol='guoapp-license-v1', serverBaseURL=SERVER,
                    distributionPublicKey=public, serverPublicKey=public,
                    activatePath='/api/v1/activate', verifyPath='/api/v1/verify', eventPath='/api/v1/events')

    def test_public_build_config_and_linker_gate(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'public.json'
            path.write_text(json.dumps(self.config()), 'utf-8')
            value = load_license_build(path)
            flags = license_linker_flags(value, '1.1.1+132')
            self.assertIn('buildLicenseRequired=true', flags)
            self.assertIn('buildAppVersion=1.1.1+132', flags)

    def test_missing_secret_or_ambiguous_endpoint_rejected(self):
        with self.assertRaises(ValueError):
            load_license_build(None)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'public.json'
            for change in ({'privateKey': 'never-embed-this'}, {'verifyPath': 'https://other.invalid/api/verify'}, {'protocol': 'unknown'}):
                value = self.config() | change
                path.write_text(json.dumps(value), 'utf-8')
                with self.assertRaises(ValueError):
                    load_license_build(path)

    def test_future_patch_and_build_increment_together(self):
        self.assertEqual(next_version('1.1.1+132'), '1.1.2+133')
        self.assertEqual(next_version('1.1.9+140'), '1.1.10+141')

    def test_customer_output_namespace_cannot_escape_or_collide(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'neutral.credentials.json'
            for customer in ('customer-a123', 'customer-b456'):
                path.write_text(json.dumps({'distributionId': customer}), 'utf-8')
                self.assertEqual(customer_distribution_id(path), customer)
            for invalid in ('../customer', 'C:/customer', '', None):
                path.write_text(json.dumps({'distributionId': invalid}), 'utf-8')
                with self.assertRaises(ValueError):
                    customer_distribution_id(path)


if __name__ == '__main__':
    unittest.main()
