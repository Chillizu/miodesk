package extplugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestManifestGrantsAndPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "p.json")
	m := Manifest{ID: "dsh", Command: "/usr/bin/true", Tools: []Grant{{Name: "dsh_read", Permission: "read"}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path, false); err != nil {
		t.Fatal(err)
	}
	m.Tools = append(m.Tools, Grant{Name: "dsh_send", Permission: "write"})
	b, _ = json.Marshal(m)
	_ = os.WriteFile(path, b, 0600)
	if _, err := loadManifest(path, false); err == nil {
		t.Fatal("write grant bypassed global write gate")
	}
	if _, err := loadManifest(path, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path, true); err == nil {
		t.Fatal("world-writable plugin manifest accepted")
	}
}
func TestManifestValidation(t *testing.T) {
	base := Manifest{ID: "mio", Command: "/bin/true", Tools: []Grant{{Name: "ok", Permission: "read"}}}
	if err := validateManifest(base, false); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.ID = "../bad" },
		func(m *Manifest) { m.Command = "sh -c echo" },
		func(m *Manifest) { m.Tools[0].Permission = "exec" },
		func(m *Manifest) { m.Tools = append(m.Tools, m.Tools[0]) },
		func(m *Manifest) { m.Tools = nil },
		func(m *Manifest) { m.Tools[0].Name = "path/escape" },
	} {
		m := base
		m.Tools = append([]Grant(nil), base.Tools...)
		mutate(&m)
		if err := validateManifest(m, false); err == nil {
			t.Fatalf("unsafe manifest accepted: %#v", m)
		}
	}
}
func TestNoPluginStartupWhenDirectoryInsecure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	mgr := Load(t.Context(), dir, false)
	if len(mgr.Plugins) != 0 || len(mgr.Tools) != 0 {
		t.Fatal("unsafe directory loaded")
	}
	mgr.Close()
}
