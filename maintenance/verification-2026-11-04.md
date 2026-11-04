# 2026-11-04 维护仓库迁入验证

验证对象：maintenance/claude-codex-compat 分支工作树，原始 HEAD 11d8df955fafc72303c6e1b29fe1e648908fe6de。origin 为用户指定的 momo-qi-gif/CliRelay。

## 结果

- 迁入的 16 个源码/测试文件与历史已部署源树逐字节一致；SOURCE_SHA256SUMS 校验全部通过。
- 从本仓库按 maintenance/build-backend.sh 指定原版本、基线 commit、构建时间重建，SHA256 为 27d8d0fe010e034e2234ed62d596fb31c9ba1f454ee761acf5fb2456f816e5ed，与当场只读核验的线上二进制完全一致。
- 主模块 go test -p 1 -json -count=1 ./...：122 个包通过，5956 个 test pass 事件（含子测试），0 失败；另有 97 个无测试包。
- go vet ./... 通过。
- updater 独立模块 test/vet/build 通过。
- 后端结构、无 vendored panel 产物检查通过；agent model 配置 Python 单测 2 项通过。
- 涉及的 Go 文件 gofmt 通过；git diff --check 通过；构建脚本 sh -n 通过。

4 项条件跳过：

1. internal/config.TestWriteYAMLFileAtomicFallsBackWhenDirectoryIsReadOnly：root 忽略目录权限。
2. internal/usage.TestRedisRuntimeLockAndQueue：未配 CLIRELAY_REDIS_TEST_ADDR。
3. internal/usage.TestPostgresRequestLogsSurviveAServerRestart：未配 CLIRELAY_POSTGRES_TEST_CONTAINER。
4. internal/usage.TestPostgresRequestLogsSurviveAnUnresponsivePrimary：同上。

## 环境和证据

Go1.27.3、CGO_ENABLED=0、Linux amd64；使用本轮专用测试 PostgreSQL，未使用生产数据库。测试工具镜像 ID sha256:3c1791e080d5df63d4e08a897f836cd645fbf053c4e20d320b63a305d47708c2。

本地日志与产物保留在被 Git 忽略的 bin/maintenance-check/ 和 bin/CLIProxyAPI-claude-stream-repair：test.json、test.stderr、vet.log、updater.log、build.log、structure.log。产物、完整测试日志和运行态数据不纳入源码版本控制。

未执行完整 golangci-lint、govulncheck、CI 指定 Go1.26.9、前端构建或本轮真实模型调用；未将上述检查称为完整 CI。线上仅只读确认版本/健康与 binary hash，未部署或修改生产。

源码/构建重现与本次维护说明已完成；当前变更尚未 commit、push。独立复审与真实 HTTP 受控验证的历史证据在运维仓库 2026-11-04 stream-repair 切换记录，本次通过 binary 精确重现建立对应关系，没有重复宣称执行这些历史检查。
