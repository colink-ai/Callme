package hermes

import (
	"os"
	"path/filepath"
	"testing"

	"callme/internal/service/agent"
)

// 端到端：snapshot → 会话期模拟 Agent 写入新 skill / memory → quarantineAfter 把新增隔离
func TestSandboxQuarantinesAgentWrittenArtifacts(t *testing.T) {
	home := t.TempDir()
	// 准备：模拟 HERMES_HOME 里已有 1 个 approved skill（应保留不动）
	approvedSkill := filepath.Join(home, "skills", "support", "approved-faq", "SKILL.md")
	mustWrite(t, approvedSkill, "approved")

	// 1) snapshot 启动前现状
	snapshotBefore(home)

	// 2) 模拟会话期 Hermes 自动写入：新 skill + memory 文件
	autoSkill := filepath.Join(home, "skills", "agent-created", "SKILL.md")
	autoMemory := filepath.Join(home, "memories", "session-x.md")
	mustWrite(t, autoSkill, "auto by agent")
	mustWrite(t, autoMemory, "session memory")

	// 3) 会话结束后隔离
	req := &agent.SessionRequest{Spec: agent.AgentSpec{HermesHome: home}, WorkDir: "/tmp/wd"}
	quarantineAfter(req)

	// 期望：已审批 skill 保留；自动生成的两个文件被移走
	if _, err := os.Stat(approvedSkill); err != nil {
		t.Fatalf("approved skill must remain: %v", err)
	}
	if _, err := os.Stat(autoSkill); !os.IsNotExist(err) {
		t.Fatalf("agent-created skill must be quarantined, but still exists")
	}
	if _, err := os.Stat(autoMemory); !os.IsNotExist(err) {
		t.Fatalf("agent-written memory must be quarantined, but still exists")
	}

	// _quarantine/<stamp>/ 下应能找到这两个文件 + SOURCE.txt
	qroot := filepath.Join(home, "_quarantine")
	entries, err := os.ReadDir(qroot)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one quarantine bucket, got entries=%v err=%v", entries, err)
	}
	bucket := filepath.Join(qroot, entries[0].Name())
	count := 0
	filepath.Walk(bucket, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			count++
		}
		return nil
	})
	if count != 3 { // 2 个隔离文件 + SOURCE.txt
		t.Fatalf("expected 3 files in quarantine bucket, got %d", count)
	}
}

// 没有快照时不应报错也不应误隔离
func TestSandboxNoSnapshotIsNoop(t *testing.T) {
	home := t.TempDir()
	autoSkill := filepath.Join(home, "skills", "x", "SKILL.md")
	mustWrite(t, autoSkill, "no snapshot")
	quarantineAfter(&agent.SessionRequest{Spec: agent.AgentSpec{HermesHome: home}})
	if _, err := os.Stat(autoSkill); err != nil {
		t.Fatalf("file must remain when no snapshot was taken: %v", err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
