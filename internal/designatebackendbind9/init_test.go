/*
Copyright 2026 Red Hat

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package designatebackendbind9

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Secret volumes project read-only files. Copying named.conf preserves that
// mode, so the init script must make its own copy writable before appending.
func TestInitScriptWithReadOnlyTSIGConfig(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "templates", "designatebackendbind9", "bin", "init.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fixtures := map[string]string{
		"config-data/default/named.conf":         "include \"/etc/named/options.conf\";\n",
		"config-data/default/named/options.conf": "options { };\n",
		"config-data/keys/rndc-key-1":            "// synthetic RNDC configuration\n",
		"predictableips/rndc_key_0":              "rndc-key-1",
		"tsig/tsigkeys.conf":                     "// synthetic TSIG configuration\n",
	}
	for name, content := range fixtures {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0440); err != nil { // #nosec G306 -- Match Kubernetes Secret file permissions for this regression test.
			t.Fatal(err)
		}
	}
	scriptPath := filepath.Join(root, "init.sh")
	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(string(script), "/var/lib", root)), 0600); err != nil { // #nosec G703 -- Path is inside t.TempDir and contains no user input.
		t.Fatal(err)
	}
	// Run twice to exercise recovery from an init container restart as well.
	for attempt := 0; attempt < 2; attempt++ {
		cmd := exec.Command("bash", scriptPath) // #nosec G204 -- Executes the repository init script against temporary fixtures.
		cmd.Env = append(os.Environ(), "POD_NAME=designate-backendbind9-pool1-0", "RNDC_PREFIX=rndc-key")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init attempt %d failed: %v\n%s", attempt, err, output)
		}
		configPath := filepath.Join(root, "config-data/merged/named.conf")
		config, err := os.ReadFile(configPath) // #nosec G304 -- Read the generated configuration inside t.TempDir.
		if err != nil {
			t.Fatal(err)
		}
		include := "include \"/etc/named/tsigkeys.conf\";"
		if strings.Count(string(config), include) != 1 {
			t.Fatalf("expected exactly one TSIG include: %s", config)
		}
		info, err := os.Stat(configPath)
		if err != nil {
			t.Fatal(err)
		}
		// Also catch this regression when tests run as root, which bypasses DAC.
		if info.Mode().Perm()&0200 == 0 {
			t.Fatal("merged named.conf is not writable by the non-root init user")
		}
		for _, name := range []string{"tsigkeys.conf", "rndc.key"} {
			content, err := os.ReadFile(filepath.Join(root, "config-data/merged/named", name)) // #nosec G304 -- Paths are fixed fixture names inside t.TempDir.
			if err != nil {
				t.Fatal(err)
			}
			fixture := "tsig/tsigkeys.conf"
			if name == "rndc.key" {
				fixture = "config-data/keys/rndc-key-1"
			}
			if string(content) != fixtures[fixture] {
				t.Fatalf("incorrect copied %s configuration", name)
			}
		}
	}
}
