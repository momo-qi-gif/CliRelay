# 同步原 CliRelay 的 main

自有 main 保存已验证的维护源码。原仓库 main 更新后，从自有 main 建同步分支，合入并验证，再合回自有 main。不要 reset --hard 到原仓库、覆盖自有 main，或反复应用旧 patch。

## 远端关系

- origin：git@github.com:momo-qi-gif/CliRelay.git（自有维护仓库）
- upstream：https://github.com/kittors/CliRelay.git（原 CliRelay）

远端不会随 clone 自动继承；首次克隆后执行：

```bash
git remote add upstream https://github.com/kittors/CliRelay.git
```

如果 upstream 已存在，先用 git remote -v 核对，不重复添加或盲目覆盖。CLIProxyAPI 是更上层独立项目，其更新需另行评估，不直接合入此远端。

## 每次同步

先确认工作树干净，且本地 main 没有未处理的分叉：

```bash
git status --short
git fetch --no-tags origin
git fetch --no-tags upstream main
git switch main
git merge --ff-only origin/main
git log --oneline main..upstream/main
# 将日期换成本次日期，名称已存在时使用新的名称
git switch -c sync/upstream-YYYYMMDD
git merge upstream/main
```

git merge 没有冲突也必须测试；有冲突时逐项判断，不能整批选择 ours/theirs。需要放弃本次同步且没有其他需保留的工作时，可先检查状态再 git merge --abort，不以强制重置替代。

## 验收门槛

1. 检查上游 release、配置、数据库 migration、依赖和工作流变化；账号、额度和日志行为需要特别核对。
2. 按 maintenance/README.md 运行完整回归、vet、updater 检查与后端构建，保留 skip/失败和未验证事项。
3. 对本仓库额外修复的 effort、工具控制、拒答、快照、交错工具、终态和请求格式检查是否被上游等效实现；保留仍有价值的回归测试。
4. 审核 `.github/workflows/` 的发布和部署触发。当前镜像发布工作流只在 kittors/CliRelay 运行；上游更新可能改动此限制。不要把 fork 的 main 合并变成未确认的镜像发布或生产部署。
5. 验证通过后提交同步结果，推送同步分支并通过 PR 或已授权的本地合并进入自有 main。发生分叉时重新检查，不强制推送。

源码合并不更新生产。新版本必须有新的源码 commit、构建时间、版本和验证记录；生产仍按另行授权、基线、备份、可回滚切换及实际验收执行。

SOURCE_SHA256SUMS 和 deployed-reference.json 是 2026-11-04 历史产物参考；后续源码更新后，该旧清单不再代表当前工作树，不应为了让旧记录“通过”而覆盖历史。为新发布新增对应记录。
