# 2026-11-04 auto 分类器生图桥接：维护仓库集成验证

本文件只记录候选源码与测试结果，不包含部署主机、内部路径、生产配置或凭证。提交、推送、部署状态应分别核对，不能由测试通过推导。

## 修改范围

- 修复分支：`fix/auto-classifier-image-bridge`，基于维护提交 `b3f1f6dfd70ad98b7742aeab1e16392b1b02571b`。
- 集成前工作区干净；0001–0007既有补丁的16个文件与验证基线逐字节一致。历史参考未随候选刷新。
- 产品代码：`internal/runtime/executor/codex_image_generation_bridge.go`；回归：`codex_auto_classifier_image_test.go`。
- 离线验证：`maintenance/verify-auto-classifier-image-guard.py`与`run-auto-classifier-image-guard-probe.py`。完整日志、二进制、临时证书和配置不入库。

识别已知security-monitor指令块、最后user完整transcript之外的已知stage1/stage2后缀，且没有显式工具/工具调用时，跳过隐式hosted image工具、guidance和强制choice。兼容计费前导system文本块，保留原始分类指令与判定。

不修改审批parser，不剥离额外text或unsolicited image，不关闭auto。正常生图、local namespace、显式hosted工具和未知格式保留旧策略。不处理普通主会话引用生图词的通用误判。

## 验证结果

- **RED→GREEN**：先只迁入回归，在旧实现上观察误注入和响应污染，再迁入实现。executor/Claude转换/thinking三包1019个test pass事件（含父子测试），3包通过，无fail/skip。
- **全套非全绿**：`go test -p 1 -json -count=1 ./...`，5983个test pass事件、121包通过。`TestManagementAPIPostgresAllRoutesSmoke`三个外网依赖子场景失败，另记父test fail；失败集合与隔离基线相同：
  - `GET_/v0/management/latest-version`：隔离网络无法访问GitHub，502。
  - `GET_/v0/management/xai-auth-url`：隔离网络无法访问xAI OAuth discovery，500。
  - `POST_/v0/management/model-openrouter-sync/run`：隔离网络无法访问OpenRouter，502。
- 没有删失败测试或开放外网来获得全绿。
- 主模块vet与仓库构建入口通过；updater模块test/vet/build通过，38个test pass事件、3包通过。
- 实际binary的20项HTTPS验证通过：两阶段、system string/blocks/计费前导、引用生图词/无关键词、block标签、额外text与unsolicited image不裁剪、正常生图stream/nonstream、本地工具及未知格式保留。
- Go1.27.3、Linux amd64、CGO_ENABLED=0。未跑指定Go1.26.9、前端、完整CI/lint/security scan或真实上游。未取得有效独立代码评审，不记为评审通过。

4项条件skip（97个无测试包skip另计）：

1. `internal/config.TestWriteYAMLFileAtomicFallsBackWhenDirectoryIsReadOnly`：root忽略目录权限。
2. `internal/usage.TestRedisRuntimeLockAndQueue`：未配置测试Redis。
3. `internal/usage.TestPostgresRequestLogsSurviveAServerRestart`：未配置故障演练容器。
4. `internal/usage.TestPostgresRequestLogsSurviveAnUnresponsivePrimary`：同上。

测试使用一次性internal Docker网络、tmpfs测试PG、回环假上游及合成凭证。客户端、服务、上游和PG均校验证书，PG为`sslmode=verify-full`；未认证请求返回401，临时容器/网络清理已核对。没有真实模型或生产数据参与。

## 复现

从已提交源码构建；有未提交改动时应明确标注dirty：

```bash
VERSION=v0.5.9-auto-classifier-image-guard \
OUTPUT=/tmp/clirelay-candidate/CLIProxyAPI-auto-classifier-image-guard \
sh maintenance/build-backend.sh
```

运行器需要Python3的`cryptography`包、Docker、本地`postgres:16-alpine`及含Python3的本地工具镜像。使用`--pull never`，不自动联网安装。默认工具镜像只是验证环境的本地标签，可用`--tools-image`指定已存在的等价镜像。

```bash
python -I maintenance/run-auto-classifier-image-guard-probe.py \
  /tmp/clirelay-candidate/CLIProxyAPI-auto-classifier-image-guard \
  --mode guarded --output-dir /tmp/clirelay-classifier-check
```

`--mode baseline`的pass仅表示符合旧版误注入预期，不表示旧版修复。

## 限制

合成上游只证明请求/响应处理行为，不能据本地耗时推算真实收益。本记录不授权生产变更、真实凭证读取、公开发布或真实模型调用。实际发布应独立记录commit、工具链、构建参数、binary hash和验证结果；内部运维状态与备份信息不放入公开补丁记录。
