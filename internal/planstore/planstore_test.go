package planstore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// testEnv isolates the Reasonix home for master-key tests and returns it.
func testEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	return home
}

func sampleFiles() PlanFiles {
	return PlanFiles{
		ID:    "plan-001",
		Steps: []string{"step one", "step two"},
		Script: "#!/usr/bin/env python\n" +
			"print('validate')",
		Scope:    []ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []ManifestEntry{{Path: "src/main.go", Action: "modify"}},
	}
}

func mustOpen(t *testing.T, ws string) *Store {
	t.Helper()
	s, err := Open(ws)
	if err != nil {
		t.Fatalf("Open(%q): %v", ws, err)
	}
	return s
}

func planDir(ws, id string) string {
	return filepath.Join(ws, ".reasonix", "plans", id)
}

// TC-PS-01 加密读写往返：写入的产物可完整读回，且磁盘 .enc 文件不含任何明文。
func TestEncryptRoundTrip(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	files := sampleFiles()
	if err := s.WritePlan(files.ID, files); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	got, err := s.ReadPlan("plan-001")
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if got.ID != "plan-001" {
		t.Errorf("ID: want plan-001, got %q", got.ID)
	}
	if !reflect.DeepEqual(got.Steps, files.Steps) {
		t.Errorf("Steps: want %v, got %v", files.Steps, got.Steps)
	}
	if got.Script != files.Script {
		t.Errorf("Script mismatch")
	}
	if !reflect.DeepEqual(got.Scope, files.Scope) {
		t.Errorf("Scope: want %v, got %v", files.Scope, got.Scope)
	}
	if !reflect.DeepEqual(got.Manifest, files.Manifest) {
		t.Errorf("Manifest: want %v, got %v", files.Manifest, got.Manifest)
	}

	dir := planDir(ws, "plan-001")
	for name, plain := range map[string]string{
		"plan.md.enc":            "step one",
		"validate.py.enc":        "print('validate')",
		"write-scope.json.enc":   "main code",
		"change-manifest.json.enc": "src/main.go",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if bytes.Contains(data, []byte(plain)) {
			t.Fatalf("%s contains plaintext %q", name, plain)
		}
	}
}

// TC-PS-02 两级密钥落盘与复用：master.key 生成于 home（0600、64 hex），项目密钥
// 密文落盘于工作区且不含主密钥明文；Close 后重新 Open 复用同一项目密钥。
func TestTwoTierKeysAndReuse(t *testing.T) {
	home := testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	s.Close()

	mkPath := filepath.Join(home, "master.key")
	mk, err := os.ReadFile(mkPath)
	if err != nil {
		t.Fatalf("master.key not created: %v", err)
	}
	txt := strings.TrimSpace(string(mk))
	if len(txt) != 64 {
		t.Fatalf("master.key: want 64 hex chars, got %d", len(txt))
	}
	if _, err := hex.DecodeString(txt); err != nil {
		t.Fatalf("master.key is not valid hex: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(mkPath)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("master.key perm: want 0600, got %v", perm)
		}
	}

	keys, err := filepath.Glob(filepath.Join(ws, ".reasonix", "keys", "*.key.enc"))
	if err != nil || len(keys) != 1 {
		t.Fatalf("want exactly 1 project key file, got %v (err %v)", keys, err)
	}
	keyEnc, err := os.ReadFile(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(keyEnc, []byte(txt)) {
		t.Fatal("project key file contains master key plaintext")
	}

	s2 := mustOpen(t, ws)
	got, err := s2.ReadPlan("plan-001")
	if err != nil {
		t.Fatalf("ReadPlan after reopen: %v", err)
	}
	if !reflect.DeepEqual(got.Steps, sampleFiles().Steps) {
		t.Fatal("reopened store could not decrypt: project key not reused")
	}
}

// TC-PS-03 项目间隔离：工作区 B 的密钥解不开工作区 A 的产物密文；A 自身可读。
func TestProjectIsolation(t *testing.T) {
	testEnv(t)
	wsA, wsB := t.TempDir(), t.TempDir()
	sA := mustOpen(t, wsA)
	if err := sA.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	sB := mustOpen(t, wsB)
	if _, err := sB.decryptFile(filepath.Join(planDir(wsA, "plan-001"), "plan.md.enc")); err == nil {
		t.Fatal("store B must not decrypt store A's ciphertext")
	}

	if _, err := sA.ReadPlan("plan-001"); err != nil {
		t.Fatalf("store A must still read its own plan: %v", err)
	}
}

// TC-PS-04 密钥损坏不静默重建：主密钥非法 hex 时 Open 报错且不覆盖；项目密钥密文
// 被篡改后重新 Open 失败；产物密文被篡改（密钥完好）时 ReadPlan 解密失败。
func TestCorruptKeysFailClosed(t *testing.T) {
	// 主密钥损坏
	home := testEnv(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "master.key"), []byte("not-hex!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ws); err == nil {
		t.Fatal("Open must fail with a corrupt master key")
	}
	if data, err := os.ReadFile(filepath.Join(home, "master.key")); err != nil || string(data) != "not-hex!" {
		t.Fatal("corrupt master key must not be overwritten")
	}

	// 项目密钥密文被篡改一个字节 → 重新 Open 失败
	t.Setenv("REASONIX_HOME", t.TempDir())
	ws2 := t.TempDir()
	s2 := mustOpen(t, ws2)
	if err := s2.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	s2.Close()
	keys, _ := filepath.Glob(filepath.Join(ws2, ".reasonix", "keys", "*.key.enc"))
	if len(keys) != 1 {
		t.Fatalf("want 1 key file, got %v", keys)
	}
	tampered, err := os.ReadFile(keys[0])
	if err != nil {
		t.Fatal(err)
	}
	tampered[len(tampered)/2] ^= 0x01
	if err := os.WriteFile(keys[0], tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ws2); err == nil {
		t.Fatal("Open must fail with a tampered project key")
	}

	// 产物密文被篡改一个字节（密钥完好）→ ReadPlan 解密失败
	t.Setenv("REASONIX_HOME", t.TempDir())
	ws3 := t.TempDir()
	s3 := mustOpen(t, ws3)
	if err := s3.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	encPath := filepath.Join(planDir(ws3, "plan-001"), "plan.md.enc")
	enc, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatal(err)
	}
	enc[len(enc)/2] ^= 0x01
	if err := os.WriteFile(encPath, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.ReadPlan("plan-001"); err == nil {
		t.Fatal("ReadPlan must fail on tampered ciphertext")
	}
}

// TC-PS-05 计划目录安全与明文侧文件：非法 plan-id 被拒绝；locks.json 为合法 JSON
// 且只含 hash/评审结论/重试计数（无产物明文）；run-state.json 初始化到 drafting。
func TestPlanDirSafetyAndPlaintextSidecars(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	for _, bad := range []string{"", "../../evil", "..\\evil", "a/b", "/abs/plan", "plan..001", "has space"} {
		if err := s.WritePlan(bad, sampleFiles()); err == nil {
			t.Fatalf("WritePlan(%q) must be rejected", bad)
		}
	}
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan(plan-001): %v", err)
	}

	locksPath := filepath.Join(planDir(ws, "plan-001"), "locks.json")
	locks, err := os.ReadFile(locksPath)
	if err != nil {
		t.Fatalf("locks.json missing: %v", err)
	}
	var lk map[string]any
	if err := json.Unmarshal(locks, &lk); err != nil {
		t.Fatalf("locks.json is not valid JSON: %v", err)
	}
	for _, key := range []string{"planId", "files", "review", "retries"} {
		if _, ok := lk[key]; !ok {
			t.Fatalf("locks.json missing field %q", key)
		}
	}
	for _, plain := range []string{"step one", "print('validate')", "main code"} {
		if bytes.Contains(locks, []byte(plain)) {
			t.Fatalf("locks.json leaks plaintext %q", plain)
		}
	}

	rsPath := filepath.Join(planDir(ws, "plan-001"), "run-state.json")
	rs, err := os.ReadFile(rsPath)
	if err != nil {
		t.Fatalf("run-state.json missing: %v", err)
	}
	var st RunState
	if err := json.Unmarshal(rs, &st); err != nil {
		t.Fatalf("run-state.json is not valid JSON: %v", err)
	}
	if st.Stage != StageDrafting {
		t.Fatalf("initial stage: want drafting, got %q", st.Stage)
	}
	if st.PlanID != "plan-001" {
		t.Fatalf("run-state planId: want plan-001, got %q", st.PlanID)
	}
}
