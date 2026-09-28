package launch

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPiPredatesEnvKeyReference pins the version that makes piAPIKeyReference
// work. pi 0.77.0 started reading a "$NAME" apiKey from the environment; the
// earlier releases of the official package would send the reference itself as
// the key, so a launch updates them first. A version it cannot read is left
// alone rather than reinstalled on every launch.
func TestPiPredatesEnvKeyReference(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"0.74.0", true},
		{"0.76.9", true},
		{"0.77.0", false},
		{"0.85.1", false},
		{"1.0.0", false},
		{"", false},
		{"not-a-version", false},
	} {
		if got := piPredatesEnvKeyReference(tc.version); got != tc.want {
			t.Errorf("piPredatesEnvKeyReference(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

// TestPiPackageInstallFromBinaryReadsVersion checks the version the update
// decision reads comes from the package.json of the pi on PATH.
func TestPiPackageInstallFromBinaryReadsVersion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib", "node_modules", "@earendil-works", "pi-coding-agent")
	bin := filepath.Join(root, "dist", "cli.js")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"` + piNpmPackage + `","version":"0.76.2"}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	install, err := piPackageInstallFromBinary(bin)
	if err != nil {
		t.Fatalf("piPackageInstallFromBinary: %v", err)
	}
	if install.packageName != piNpmPackage || install.version != "0.76.2" {
		t.Fatalf("install = %+v, want %s at 0.76.2", install, piNpmPackage)
	}
}
