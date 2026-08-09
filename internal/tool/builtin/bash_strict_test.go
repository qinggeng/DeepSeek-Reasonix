package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/planmode"
	"reasonix/internal/planstore"
	"reasonix/internal/sandbox"
	"reasonix/internal/secrets"
)

// TC-SB-01 + TC-SB-04：strict 模式下 ForbidReadRoots 追加 reasonix home
// （master.key 所在）与 .reasonix/plans 目录，且幂等；Mode/WriteRoots 不被改动。
func TestStrictBashSpecForbidsKeyLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	ws := t.TempDir()

	spec := sandbox.Spec{Mode: "enforce", WriteRoots: []string{ws}}
	got := strictBashSpec(spec, ws)
	if len(got.ForbidReadRoots) != 2 {
		t.Fatalf("want 2 forbid roots, got %v", got.ForbidReadRoots)
	}
	want := []string{planstore.HomeDir(), planstore.PlansDir(ws)}
	for _, w := range want {
		found := false
		for _, f := range got.ForbidReadRoots {
			if filepath.Clean(f) == filepath.Clean(w) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ForbidReadRoots missing %q (got %v)", w, got.ForbidReadRoots)
		}
	}
	if got.Mode != spec.Mode || len(got.WriteRoots) != len(spec.WriteRoots) {
		t.Fatal("strictBashSpec must not alter Mode/WriteRoots")
	}
	// 幂等：重复调用不重复追加
	if got2 := strictBashSpec(got, ws); len(got2.ForbidReadRoots) != 2 {
		t.Fatalf("strictBashSpec must be idempotent, got %v", got2.ForbidReadRoots)
	}
}

// TC-SB-02 + TC-SB-05：ExecuteDetailed 集成——strict ctx 时实际传给
// sandbox.Command 的 spec 携带 ForbidReadRoots 增强（进程起始目录恒为工作区
// workDir，spec.Mode 不被改动）；非 strict 时 spec 原样。
func TestBashStrictModeSpecApplied(t *testing.T) {
	t.Setenv("REASONIX_HOME", t.TempDir())
	ws := t.TempDir()

	orig := bashSandboxCommand
	defer func() { bashSandboxCommand = orig }()
	var captured sandbox.Spec
	bashSandboxCommand = func(spec sandbox.Spec, sh sandbox.Shell, cmd string) ([]string, bool) {
		captured = spec
		return []string{sh.Path}, false
	}

	b := bash{workDir: ws}
	ctx := planmode.WithStrict(context.Background(), true)
	b.ExecuteDetailed(ctx, json.RawMessage(`{"command":"echo hi"}`))
	if len(captured.ForbidReadRoots) != 2 {
		t.Fatalf("strict bash must carry forbid roots, got %v", captured.ForbidReadRoots)
	}
	if captured.Mode != "" {
		t.Fatalf("strict must not change Mode, got %q", captured.Mode)
	}

	captured = sandbox.Spec{}
	b.ExecuteDetailed(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if len(captured.ForbidReadRoots) != 0 {
		t.Fatalf("non-strict bash must not carry forbid roots, got %v", captured.ForbidReadRoots)
	}
}

// TC-SB-03：子进程环境不携带主密钥相关凭据——注册的凭据 key 在 bash 子进程
// 环境中被剥离（主密钥从不以环境变量形式进入子进程）。
func TestBashCommandEnvStripsCredentials(t *testing.T) {
	const credKey = "REASONIX_TEST_CRED_PLANMASTER"
	secrets.RegisterCredentialEnvKeys([]string{credKey})
	t.Setenv(credKey, "hex64-master-key-material-that-must-not-leak")

	env := bashCommandEnv(context.Background())
	for _, item := range env {
		if strings.HasPrefix(item, credKey+"=") {
			t.Fatalf("bash subprocess env leaks credential key %s", credKey)
		}
	}
}
