# Memoh 定制版

上游：[felinics/Memoh](https://github.com/felinics/Memoh)。当前整合至 `3ed0c2c`（2026-09-10），保留上游历史和 AGPL-3.0 许可。

配套仓库：[各系统 Runtime 客户端](https://github.com/shenmintao/memoh-clients) · [Android App](https://github.com/shenmintao/memoh-android)。

## 定制功能

- 绑定到 Bot 的远端 Runtime 提供 MCP/Skills；本机凭据不随能力发现上传。
- Runtime 版本上报、远端工作目录、ACP 的运行中补充消息，以及 Android 旧协议兼容。
- 按各模型声明支持的原生最高档提供思考强度，不用额外提示词模拟“更多思考”。
- 长任务在上下文压力下释放已完成工具循环，同时保留补充指令与最新必要结果。
- 参考 deepseek-harness：达到输入预算 80% 后才允许循环内裁剪；平时保留已发送前缀。后台任务仅追加状态变化，固定开始时间和排序。
- 补齐缓存未命中 token 的统计，避免只统计命中而产生虚高比例。

## 上游整合

这次同步包含 DeepSeek 思考回放、压缩兜底、上下文用量面板、运行中补充与后续队列、工作区依赖管理等改进。上游已更改外部 Agent/工作区初始化和数据库迁移；已有部署升级前需完整备份并在测试环境跑迁移。

## 发布

1. `git clone --recurse-submodules https://github.com/shenmintao/memoh.git`。
2. 参考上游 README 配置本地开发环境；运行 `go test ./...`、`golangci-lint run ./...`，以及 `pnpm install --frozen-lockfile` 后的前端检查。
3. 同步上游：`git fetch upstream`，`git merge upstream/main`。新 clone 需先 `git remote add upstream https://github.com/felinics/Memoh.git`。解决冲突后重点回归队列、ACP、MCP 鉴权与缓存。
4. 更新定制版本说明，推送版本标签；手动运行 Custom Release，上传 Linux amd64/arm64 server/channel/bridge/mcp、完整源码及 SHA256SUMS 到草稿 Release。二进制写入版本标签、提交号和构建时间。Web 镜像按上游 Dockerfile 构建，不能仅替换后端来完成跨版本升级。
5. 用测试数据库与测试工作区确认迁移、初始化和端到端功能后发布。服务端部署与 GitHub 源码发布是独立动作；不要把生产 config.toml、数据库、令牌、会话内容或 `.memoh` 目录加入 Git。

原上游 npm 发布 workflow 保存在本目录供同步参考，定制仓库不向上游 npm 命名空间发布。
上游 AGENTS.md 与模型能力定时任务在定制仓库默认不运行，维护者仍可手动触发。

## 本次验证（2026-09-10）

- 合并后 Go 全量测试、ACP/会话运行时 race 检查、golangci-lint 与 Linux amd64 服务端构建通过。
- Web 生产构建通过。Web 类型检查仍有上游已知积压；上游 CI 已将该步骤设为仅报告。`apps/web`、`apps/desktop` 和 UI 子模块与本次上游版本一致。
- Windows 全量 Vitest：1559 项通过，13 项失败、另有测试文件加载失败。失败包括未启动 Docker、未先构建 Runtime、上游桌面 URL 断言、浏览器存储/mock 和 Windows SVG 路径问题。该检查没有作为全量通过报告。
- 配套客户端在 Windows/macOS/Linux 的 CI，以及 Pi/Codex 适配器 CI 通过；Android 单元测试、Lint 和 APK 构建通过。

2026-09-10 已完成一次现有部署升级验证，运行代码为 `d740ed08`：数据库从 141 迁移到 148，Server/Channel/Web 同步升级，原工作区和远端客户端恢复，Codex/Pi ACP 启动及 NAS MCP 只读调用通过。网页资源与发布构建一致。

存量自管理 Codex ACP 在迁移前转换为通用 ACP（命令仍为 `codex-acp`），保留原 Agent ID、会话与工作区登录文件，避免上游 0144 归档这些会话。其他部署升级时须先核对实际配置：同一 Bot 已配置通用 ACP、使用托管 API Key 或 OAuth 的情况不能直接套用该转换。工作区沿用原镜像并固定摘要，新基础镜像与原生直连 Agent 的迁移仍按上游升级说明单独验证。

## 缓存验证

固定合成工具结果，使用 SDK 实际序列化请求在 DeepSeek `deepseek-flash` 上对比。旧版后两次请求改写前缀，缓存命中率约 21%；修复版保持前缀，约 85%–87%。不代表所有生产会话都能达到该比例；冷请求、工具/系统配置变化和必要压缩仍会降低命中。

参考 [deepseek-harness 请求前缀回归](https://github.com/deepseek-ai/deepseek-harness/blob/aa8262ec091698bae9a6b04773a6b5b06ad4aef2/packages/core/agent-loop/tests/request-reconstruction.spec.ts) 与 [压力触发压缩](https://github.com/deepseek-ai/deepseek-harness/blob/aa8262ec091698bae9a6b04773a6b5b06ad4aef2/packages/compaction/compaction-basic/src/index.ts)。
