# Claude Code → Codex 补丁维护

此分支维护当前部署所用的源码修复。原始基线为 `11d8df955fafc72303c6e1b29fe1e648908fe6de`（CliRelay v0.5.9 发行线）。源码和回归测试是维护对象，不需要再应用旧目录中的 patch。

## 已纳入修复

| 原补丁序号 | 内容 | 主要位置 |
|---|---|---|
| 0001 | 保留消息内 system role | codex/claude 请求转换与 request_test |
| 0002 | output_config.effort 正确传递，不被 adaptive 强制覆盖 | 请求转换及 executor effort 回归 |
| 0003 | 请求日志提取显式思考深度 | internal/thinking/level.go 及 executor 日志测试 |
| 0004 | 工具选择与禁并行控制 | 请求转换与 request_tool_control_test |
| 0005 | 拒答保留、长度截断与其他终态分类 | 响应转换、codex executor/responses 及 terminal 回归 |
| 0006 | 流式快照补全、交错工具独立参数、统一 FIFO、截断工具保护 | 响应转换、stream_repair/review_regression 测试 |
| 0007 | 字符串顶层 system 与 URL 图片兼容 | 请求转换与 request_format_compat 测试 |

上述相对路径在 `internal/translator/codex/claude/` 与 `internal/runtime/executor/` 下。无关调度、账号、数据库 schema、前端保持基线。

## 行为边界

- 日志中的 thinking_level 是客户端请求值，不代表最终上游 normalization 或管理员 payload override 后的 effort。
- 工具块等 item.done 或 terminal，且参数为合法 JSON 对象后才下发；包括首个工具在内的开始可能延迟。非法/截断工具参数不作为可执行 tool_use。
- 文本/拒答快照只补已有前缀后的内容；冲突快照不猜测恢复。
- deferred 文本、拒答、thinking 使用同一 FIFO，先回放再补 terminal 快照后缀。
- Claude 长度截断返回 max_tokens；其他 incomplete 保持明确错误；Responses 直通合同另有回归测试。
- 未注册的终态工具、无稳定身份的并行事件等不保证完整恢复。

## 构建

项目 CI 声明 Go1.26.9；已部署参考产物实际由 Go1.27.3 构建。使用 `sh maintenance/build-backend.sh` 生成 Linux amd64 静态后端，产物默认在已忽略的 bin/ 中。不构建前端，不自动部署。

通常保留默认 BUILD_COMMIT=当前 HEAD，便于未来发布追踪。若需重现 2026-11-04 已部署参考产物，必须使用该记录所列完整源码与 Go1.27.3，并指定：

```bash
VERSION=v0.5.9-claude-stream-repair \
BUILD_COMMIT=11d8df955fafc72303c6e1b29fe1e648908fe6de \
BUILD_DATE=2026-11-04T06:46:08Z \
sh maintenance/build-backend.sh
```

历史 binary 的 Commit 字段只是原始基线，不是补丁后的提交；请同时使用参考记录中的 patch/source/binary hash 辨识，不把它当作无补丁版本。

## 验证

```bash
go test -p 1 -count=1 ./...
go vet ./...
(cd updater && go test -count=1 ./... && go vet ./... && go build ./...)
```

完整 PG 集成测试需向隔离测试数据库设置 `CLIRELAY_POSTGRES_TEST_DSN`；部分测试会操作测试数据库，不使用生产 DSN。Redis、Postgres 故障切换测试另需其专用环境变量，未配置时会 skip。测试应记录 skip，不能把未执行项目称为通过。完整 CI 入口为 scripts/ci-pr.sh，含其他检查；上面的命令不等价于完整 CI。

发布记录见 [deployed-reference.json](deployed-reference.json)。`SOURCE_SHA256SUMS` 固定记录本次迁入的 16 个源码/测试文件，可在仓库根目录执行 `sha256sum -c maintenance/SOURCE_SHA256SUMS` 与本次部署源码对照；后续修改后不应盲目刷新该历史参考，应另建对应发布记录。线上访问、凭证和部署配置不纳入仓库。当前源码纳入本仓库不代表自动授权 commit、push、发布镜像或切换生产。

## 后续同步

- 保持发行基线与维护分支可追踪；同步新版本时逐项检查上述补丁是否已被替代。
- 每次修复同时维护回归测试，优先验证实际 HTTP 转换链。
- 新产物使用新的版本、构建时间和源码 commit；不要复用历史标签伪装成原二进制。
- 运维备份、切换和回滚证据留在运维文档仓库；本仓库不存凭证、生产 config、DB dump、账号文件或日志正文。
