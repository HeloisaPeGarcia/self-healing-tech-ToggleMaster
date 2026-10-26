import json, shutil, subprocess, sys, tempfile, unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "render_config.py"
SRC = ROOT / "gitops" / "services" / "self-healing"
RULES = "AuthHigh5xxRate|auth-service|service=auth-service\nOutro|flag-service"


def run(rules, cooldown="300", d=None, extra=()):
    return subprocess.run([sys.executable, str(SCRIPT), "--rules", rules, "--cooldown", cooldown,
                           "--dir", str(d), *extra], capture_output=True, text=True)


class RenderConfig(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        shutil.copytree(SRC, self.tmp / "sh")
        self.d = self.tmp / "sh"

    def tearDown(self):
        shutil.rmtree(self.tmp)

    def test_generates_config_and_rbac_consistently(self):
        r = run(RULES, "120", self.d)
        self.assertEqual(r.returncode, 0, r.stderr)
        cfg = json.loads((self.d / "config.json").read_text())
        self.assertEqual(cfg["cooldownSeconds"], 120)
        self.assertEqual(cfg["rules"][0]["match"], {"service": "auth-service"})
        self.assertNotIn("match", cfg["rules"][1])
        self.assertIn('resourceNames: ["auth-service", "flag-service"]', (self.d / "rbac.yaml").read_text())

    def test_invalid_inputs_write_nothing(self):
        before = ((self.d / "config.json").read_text(), (self.d / "rbac.yaml").read_text())
        bad = [("A|Auth_Service", "300"), ("A|auth-service", "5"), ("A|auth-service", "abc"),
               ("A|auth-service|x", "300"), ("A;rm -rf|auth-service", "300"), ("", "300"),
               ("A|auth-service\nA|auth-service", "300"), ("A|auth-service|k=$(id)", "300"),
               ("A|*", "300"), ("A|auth-service|1bad=v", "300")]
        for rules, cd in bad:
            with self.subTest(rules=rules, cd=cd):
                r = run(rules, cd, self.d)
                self.assertEqual(r.returncode, 2, r.stdout)
                self.assertIn("ERRO", r.stderr)
        after = ((self.d / "config.json").read_text(), (self.d / "rbac.yaml").read_text())
        self.assertEqual(before, after)

    def test_dry_run_does_not_write(self):
        before = (self.d / "config.json").read_text()
        r = run(RULES, "300", self.d, ("--dry-run",))
        self.assertEqual(r.returncode, 0)
        self.assertEqual(before, (self.d / "config.json").read_text())

    def test_idempotent(self):
        run(RULES, "300", self.d)
        first = ((self.d / "config.json").read_text(), (self.d / "rbac.yaml").read_text())
        run(RULES, "300", self.d)
        self.assertEqual(first, ((self.d / "config.json").read_text(), (self.d / "rbac.yaml").read_text()))

    def test_default_rules_reproduce_committed_files(self):
        default = "AuthHigh5xxRate|auth-service\nToggleMasterHTTPHealthDown|auth-service|service=auth-service"
        run(default, "300", self.d)
        self.assertEqual(json.loads((self.d / "config.json").read_text()), json.loads((SRC / "config.json").read_text()))
        self.assertEqual((self.d / "rbac.yaml").read_text(), (SRC / "rbac.yaml").read_text())


if __name__ == "__main__":
    unittest.main()
