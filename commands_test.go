package pbnats

import (
	"path/filepath"
	"strings"
	"testing"

	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

func TestResolveIn(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("some", "dir"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	absOther, err := filepath.Abs(filepath.Join("elsewhere", "jetstream"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	tests := []struct {
		name    string
		baseDir string
		path    string
		want    string
	}{
		{
			name:    "empty base leaves the path alone for stdout previews",
			baseDir: "",
			path:    "operator.jwt",
			want:    "operator.jwt",
		},
		{
			name:    "relative path is joined onto the base",
			baseDir: abs,
			path:    "operator.jwt",
			want:    filepath.ToSlash(filepath.Join(abs, "operator.jwt")),
		},
		{
			name:    "dot-relative path is cleaned and joined",
			baseDir: abs,
			path:    "./storage/jetstream",
			want:    filepath.ToSlash(filepath.Join(abs, "storage", "jetstream")),
		},
		{
			name:    "an explicit absolute path is never rewritten",
			baseDir: abs,
			path:    absOther,
			want:    absOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveIn(tt.baseDir, tt.path); got != tt.want {
				t.Errorf("resolveIn(%q, %q) = %q, want %q", tt.baseDir, tt.path, got, tt.want)
			}
		})
	}
}

// resolveIn must not emit backslashes: NATS config strings treat the backslash
// as an escape character, so a Windows path would be silently mangled.
func TestResolveInUsesForwardSlashes(t *testing.T) {
	abs, err := filepath.Abs("natsconf")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if got := resolveIn(abs, "jwt"); strings.Contains(got, `\`) {
		t.Errorf("resolveIn returned a backslash path: %q", got)
	}
}

// The generated config must be usable from any working directory, which is the
// entire reason these paths are resolved. Guard against a regression to './'.
func TestGeneratedConfigPathsAreAbsolute(t *testing.T) {
	outputDir, err := filepath.Abs(filepath.Join("tmp", "nats-config"))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	operator := &pbtypes.SystemOperatorRecord{Name: "test", PublicKey: "OTEST", JWT: "jwt"}
	sysAccount := &systemAccountInfo{PublicKey: "ATEST", JWT: "sysjwt"}

	opConf := generateOperatorConf(operator, sysAccount, outputDir)
	wantOperator := "operator: '" + filepath.ToSlash(filepath.Join(outputDir, "operator.jwt")) + "'"
	if !strings.Contains(opConf, wantOperator) {
		t.Errorf("operator.conf missing absolute operator path %q:\n%s", wantOperator, opConf)
	}
	if strings.Contains(opConf, "'./operator.jwt'") {
		t.Error("operator.conf still uses a CWD-relative operator path")
	}

	natsConf := generateNATSConf("test-server", 4222, "./storage/jetstream", 9222, outputDir)
	wantJWTDir := "dir: '" + filepath.ToSlash(filepath.Join(outputDir, "jwt")) + "'"
	if !strings.Contains(natsConf, wantJWTDir) {
		t.Errorf("nats.conf missing absolute resolver dir %q:\n%s", wantJWTDir, natsConf)
	}
	wantStore := "store_dir: '" + filepath.ToSlash(filepath.Join(outputDir, "storage", "jetstream")) + "'"
	if !strings.Contains(natsConf, wantStore) {
		t.Errorf("nats.conf missing absolute store_dir %q:\n%s", wantStore, natsConf)
	}
}

// The stdout preview flags have no directory to resolve against and must keep
// emitting relative paths.
func TestPreviewConfigStaysRelative(t *testing.T) {
	operator := &pbtypes.SystemOperatorRecord{Name: "test", PublicKey: "OTEST", JWT: "jwt"}
	sysAccount := &systemAccountInfo{PublicKey: "ATEST", JWT: "sysjwt"}

	if got := generateOperatorConf(operator, sysAccount, ""); !strings.Contains(got, "operator: 'operator.jwt'") {
		t.Errorf("preview operator.conf should keep a relative path:\n%s", got)
	}
	if got := generateNATSConf("test-server", 4222, "./storage/jetstream", 9222, ""); !strings.Contains(got, "dir: 'jwt'") {
		t.Errorf("preview nats.conf should keep a relative resolver dir:\n%s", got)
	}
}
