package pbnats

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
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

	natsConf := generateNATSConf(natsConfParams{ServerName: "test-server", Port: 4222, WebsocketPort: 9222, JetstreamStoreDir: "./storage/jetstream"}, outputDir)
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
	if got := generateNATSConf(natsConfParams{ServerName: "test-server", Port: 4222, WebsocketPort: 9222, JetstreamStoreDir: "./storage/jetstream"}, ""); !strings.Contains(got, "dir: 'jwt'") {
		t.Errorf("preview nats.conf should keep a relative resolver dir:\n%s", got)
	}
}

// The leaf node listener and the JetStream domain are opt-in, and when set they
// land where nats-server reads them. Checked by parsing the file with
// nats-server's own config parser, not by matching text: a block that only
// looked right would pass a string check and fail when the server starts.
func TestNATSConfLeafnodesAndDomain(t *testing.T) {
	parse := func(t *testing.T, p natsConfParams) *server.Options {
		t.Helper()
		dir := t.TempDir()
		// nats.conf includes operator.conf. An empty one is enough to parse.
		if err := os.WriteFile(filepath.Join(dir, "operator.conf"), nil, 0644); err != nil {
			t.Fatal(err)
		}
		confPath := filepath.Join(dir, "nats.conf")
		if err := os.WriteFile(confPath, []byte(generateNATSConf(p, dir)), 0644); err != nil {
			t.Fatal(err)
		}
		opts, err := server.ProcessConfigFile(confPath)
		if err != nil {
			t.Fatalf("nats-server cannot parse the generated nats.conf: %v", err)
		}
		return opts
	}
	base := natsConfParams{ServerName: "hub", Port: 4222, WebsocketPort: 9222, JetstreamStoreDir: "js"}

	t.Run("zero values write neither", func(t *testing.T) {
		opts := parse(t, base)
		if opts.LeafNode.Port != 0 {
			t.Errorf("leafnode port = %d, want 0 (no leafnodes block)", opts.LeafNode.Port)
		}
		if opts.JetStreamDomain != "" {
			t.Errorf("JetStream domain = %q, want none", opts.JetStreamDomain)
		}
	})

	t.Run("set values are what the server reads", func(t *testing.T) {
		p := base
		p.LeafnodePort = 7422
		p.JetstreamDomain = "hub"
		opts := parse(t, p)
		if opts.LeafNode.Port != 7422 {
			t.Errorf("leafnode port = %d, want 7422", opts.LeafNode.Port)
		}
		if opts.JetStreamDomain != "hub" {
			t.Errorf("JetStream domain = %q, want %q", opts.JetStreamDomain, "hub")
		}
	})
}

// The export command lives in someone else's binary, so its flag names share a
// namespace with the host application's. `--config` was the collision that
// actually happened: a PocketBase host defining `--config <path>` as a
// persistent root flag, cobra letting this subcommand's local bool shadow the
// root's string, and `myapp --config ./app.yaml nats export -o ./out` therefore
// setting the PREVIEW flag, swallowing the path as a positional argument,
// printing nats.conf to stdout and creating no files at all. Exit status zero.
//
// The flags are now named for the artifacts they produce. This test is a
// tripwire on that: any future flag whose name a host is likely to have taken —
// `config`, `dir`, `dev`, `debug` — is the same bug waiting to happen.
func TestExportFlagsDoNotCollideWithCommonHostFlags(t *testing.T) {
	cmd := createExportCommandWithOptions(nil, DefaultCommandOptions())

	// Names a PocketBase host is likely to define on its root command. PocketBase
	// itself ships --dir, --dev, --encryptionEnv and --queryTimeout; --config is
	// near-universal in applications that read a config file.
	for _, name := range []string{"config", "dir", "dev", "debug", "http", "encryptionEnv", "queryTimeout"} {
		if f := cmd.Flags().Lookup(name); f != nil {
			t.Errorf("export defines --%s, which collides with a flag a host application is likely to own; "+
				"name flags after the artifact they produce", name)
		}
	}

	// The replacement must exist, or the rename silently removed a capability.
	for _, name := range []string{"output", "operator-jwt", "nats-conf", "operator-conf"} {
		if f := cmd.Flags().Lookup(name); f == nil {
			t.Errorf("export is missing --%s", name)
		}
	}
}

// A preview flag prints one artifact and writes nothing. Combined with an
// explicit --output the caller has asked for two contradictory things, and the
// silent outcome — text on the terminal, an empty directory, exit 0 — is exactly
// what the --config collision produced. Refusing makes the next such collision
// loud instead.
func TestExportRefusesPreviewFlagsAlongsideOutput(t *testing.T) {
	for _, preview := range []string{"operator-jwt", "nats-conf", "operator-conf"} {
		cmd := createExportCommandWithOptions(nil, DefaultCommandOptions())
		cmd.SetArgs([]string{"--output", t.TempDir(), "--" + preview})
		cmd.SetOut(&strings.Builder{})
		cmd.SetErr(&strings.Builder{})

		err := cmd.Execute()
		if err == nil {
			t.Errorf("--%s with --output was accepted; it writes no files, so this must be an error", preview)
			continue
		}
		if !strings.Contains(err.Error(), preview) || !strings.Contains(err.Error(), "--output") {
			t.Errorf("--%s with --output: error should name both flags, got %q", preview, err)
		}
	}
}

// Without --output and without a preview flag there is nothing to do, and the
// error has to say so rather than bootstrapping the app first. This also pins
// that the check runs before app.Bootstrap() — the test passes a nil app, so a
// reordering that bootstraps first would panic here rather than fail politely.
func TestExportRequiresOutputOrAPreviewFlag(t *testing.T) {
	cmd := createExportCommandWithOptions(nil, DefaultCommandOptions())
	cmd.SetArgs(nil)
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("export with no flags was accepted")
	}
	if !strings.Contains(err.Error(), "--output") {
		t.Errorf("error should name --output, got %q", err)
	}
}
