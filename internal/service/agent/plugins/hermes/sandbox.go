// 自学习沙箱：可选的强隔离工具，用于阻断 Hermes 自动写入 skill / memory。
//
// 背景：Hermes ACP 默认开启 skill_manage / memory 工具，且 HERMES_HOME 跨会话共享。
// 当前默认治理策略是"保留 Hermes 原生目录 + Callme 审计轨记录变化"；本文件保留为
// 高风险部署可启用的硬隔离能力。在不改 Hermes 源码的前提下，它的工作方式是：
//
//  1. 启动前 snapshot —— 记录 HERMES_HOME 下 skills/ 与 memories/ 的现存文件清单。
//  2. 会话结束后 quarantine —— 把本次会话期【新增】的 SKILL.md / memory 文件
//     原子移动到 HERMES_HOME/_quarantine/<时间戳>/，附带 SOURCE.txt 标注来源会话。
//
// 这样：已审批的 skill 仍可被 Hermes 加载使用；Hermes 自动创建的产物在下一次会话前已被
// 移走，不会进入回答链路；管理员可在 _quarantine 中审计、提取为候选资产再走审批闸门。
package hermes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"callme/internal/service/agent"
	"callme/internal/service/agent/plugins/acp"

	"go.uber.org/zap"
)

// 监控目录：Hermes 自学习产物默认落在这些子目录
var watchedSubdirs = []string{"skills", "memories"}

// snapshots 维持每个 HERMES_HOME 的"会话开始前"文件清单。
// key 用绝对路径，去抖共享 HERMES_HOME 下多个会话先后启动时的并发情况。
var (
	snapshotMu sync.Mutex
	snapshots  = map[string]map[string]struct{}{}
)

// snapshotBefore 在会话启动前抓取 HERMES_HOME 下受监控目录的现存文件清单。
// 多次启动只保留最早一次（粗粒度足够：我们只关心"曾经稳定可加载的资产"）。
func snapshotBefore(home string) {
	if home == "" {
		return
	}
	abs := absPath(home)
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	if _, ok := snapshots[abs]; ok {
		return // 已有更早的快照，沿用之
	}
	files, err := listWatchedFiles(abs)
	if err != nil {
		acp.LogWarn("Hermes sandbox: snapshot HERMES_HOME failed",
			zap.String("home", abs), zap.Error(err))
		return
	}
	snapshots[abs] = files
	acp.LogInfo("Hermes sandbox: snapshot taken",
		zap.String("home", abs), zap.Int("existingFiles", len(files)))
}

// quarantineAfter 在会话结束后把【新增的】skill / memory 文件隔离到 _quarantine。
// 注意：会话期间用户可能开了多个并发会话，这里只识别"快照之后新增"，
// 即使有并发，最坏情况是把另一个仍在跑的会话的产物也隔离掉——
// 但生产 Agent 本就不该写 skill/memory，被隔离也是预期效果。
func quarantineAfter(req *agent.SessionRequest) {
	if req == nil || req.Spec.HermesHome == "" {
		return
	}
	abs := absPath(req.Spec.HermesHome)

	snapshotMu.Lock()
	before, ok := snapshots[abs]
	snapshotMu.Unlock()
	if !ok {
		acp.LogDebug("Hermes sandbox: no snapshot, skip quarantine", zap.String("home", abs))
		return
	}

	current, err := listWatchedFiles(abs)
	if err != nil {
		acp.LogWarn("Hermes sandbox: scan HERMES_HOME failed",
			zap.String("home", abs), zap.Error(err))
		return
	}
	var added []string
	for path := range current {
		if _, existed := before[path]; !existed {
			added = append(added, path)
		}
	}
	if len(added) == 0 {
		return
	}

	stamp := time.Now().Format("20060102-150405")
	bucket := filepath.Join(abs, "_quarantine", stamp)
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		acp.LogError("Hermes sandbox: create quarantine bucket failed",
			zap.String("path", bucket), zap.Error(err))
		return
	}

	moved := 0
	for _, src := range added {
		rel, _ := filepath.Rel(abs, src)
		dst := filepath.Join(bucket, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			acp.LogWarn("Hermes sandbox: mkdir for quarantine target failed",
				zap.String("dst", dst), zap.Error(err))
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			acp.LogWarn("Hermes sandbox: move to quarantine failed",
				zap.String("src", src), zap.String("dst", dst), zap.Error(err))
			continue
		}
		moved++
	}

	// 留个 SOURCE.txt 便于管理员追溯
	source := fmt.Sprintf("session_id: \nfrom_workdir: %s\nat: %s\nfiles: %d\n",
		req.WorkDir, time.Now().Format(time.RFC3339), moved)
	if err := os.WriteFile(filepath.Join(bucket, "SOURCE.txt"), []byte(source), 0o644); err != nil {
		acp.LogWarn("Hermes sandbox: write SOURCE.txt failed", zap.Error(err))
	}

	// 隔离完成后清理快照，下次启动重新抓取（这样新审批通过移入 skills/ 的资产能被纳入）
	snapshotMu.Lock()
	delete(snapshots, abs)
	snapshotMu.Unlock()

	acp.LogInfo("Hermes sandbox: quarantined agent-written artifacts",
		zap.String("home", abs),
		zap.String("bucket", bucket),
		zap.Int("moved", moved))
}

// listWatchedFiles 列出 HERMES_HOME 下 skills/ 与 memories/ 中的所有常规文件（绝对路径）。
// 不存在的目录返回空集；忽略 _quarantine 自身，避免递归。
func listWatchedFiles(home string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	for _, sub := range watchedSubdirs {
		root := filepath.Join(home, sub)
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // 忽略个别条目错误，尽力扫描
			}
			if d.IsDir() {
				return nil
			}
			out[path] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
