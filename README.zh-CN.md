# llmTools / AI Worker

[English](README.md) | [简体中文](README.zh-CN.md)

llmTools 通过 MCP 将 Codex 连接到 **Pi**。Codex 负责需求、项目决策和最终验收，Pi 执行委派的工作，llmTools 管理角色、已授权工作区、会话、限制和任务生命周期。**目前仅支持 Pi 运行时。**

Codex 通过单个 `prompt` 提交需求，llmTools 原样转发。任务完成后，智能体的回复也原样返回，包括 Markdown、空白和看起来像 JSON 的文本。路由、工作区授权和控制状态作为独立的协议元数据传递。Pi 管理服务商凭据、模型配置和会话历史；llmTools 不提供服务商编辑器，也不存储这些凭据。

## 桌面端配置

1. 安装并配置 Pi，完成服务商认证，配置需要使用的模型。
2. 打开 AI Worker，在 **Runtimes（运行时）** 中选择 Pi 可执行文件和用于持久保存 Pi 会话的目录，然后保存。
3. 点击 **Check Pi（检查 Pi）**，验证可执行文件和离线 RPC 协议。此检查不会调用模型。Pi 会在执行任务时自动启动，无需启动或停止服务器，也无需配置端点或端口。
4. 在 **Agents（智能体）** 中刷新 Pi 模型列表，为各角色选择模型，启用允许 Codex 使用的角色，然后保存路由。Pi 不使用命名智能体选择器。
5. 在 **Settings → Authorized Workspaces（设置 → 已授权工作区）** 中授权项目目录。
6. 在 **Codex Integration（Codex 集成）** 中注入委派 Skill，将 MCP 配置复制到 Codex，并执行 **Probe MCP（探测 MCP）**。修改角色暴露设置或替换可执行文件后，需要重新连接 Codex 中的 MCP。

**Check Pi** 验证运行时通信是否可用，**Probe MCP** 验证 MCP 协议和已暴露的工具。两者都不能证明服务商凭据或所选模型能够完成真实任务；请从 Codex 委派一个小任务，单独验证实际调用。

## 角色

| 角色 | MCP 工具 | 访问权限 | 职责 |
| --- | --- | --- | --- |
| Developer / 完整实现 | `delegate_developer` | 项目读写、编辑和沙箱命令执行 | 实现需求、运行测试并修复失败，由 Codex 验收 |
| Implementation / 实现 | `delegate_implementation` | 仓库只读 | 提供实现建议和候选修改方案 |
| Review / 审查 | `delegate_review` | 仓库只读 | 独立审查 |
| Research / 研究 | `delegate_research` | 仅使用提供的上下文 | 研究所提供的信息 |

只有已暴露的角色才会出现在 MCP 的 `tools/list` 中。是否暴露由配置决定，与运行时健康状态相互独立。未配置的角色不会显示。继续和取消工具仍可用于历史任务，但原运行时和会话必须仍受支持。

每个任务会固定逻辑角色、Pi 模型、工作区和能力策略。修改路由只影响新任务；受支持且处于挂起状态的 Pi 任务会按原设置继续。调用方不能通过传入能力标志或路径来扩大角色权限。

### 完整实现与测试

需要智能体实际修改项目并执行测试时，应使用 Developer。可以要求它返回修改的文件、实际执行的测试命令及结果，以及剩余问题。修改会直接写入已授权项目，Codex 应检查这些修改和测试证据后再验收。

Developer 可以使用 llmTools 提供的 `repo_read`、`repo_glob`、`repo_grep`、`repo_write`、`repo_edit` 和 `repo_exec`。Implementation 和 Review 只有读取工具，Research 没有仓库访问权限。委派执行期间禁用 Pi 原生 shell/写入工具、用户扩展、上下文文件自动发现、网络访问和子智能体。

文件操作限制在所选工作区内。单次文件写入或编辑上限为 1 MiB；精确编辑要求待替换内容只匹配一处。禁止访问工作区外路径、通过符号链接越界，以及写入项目根目录的 `.git`。

项目命令目前依赖 macOS 的 `sandbox-exec`；不支持的环境会返回 `EXECUTION_SANDBOX_UNAVAILABLE`。命令只能写入项目目录和任务私有临时目录，可以读取系统工具及已安装的依赖缓存。禁止向外建立网络连接，包括连接 localhost 服务；测试可以绑定回环监听端口，但沙箱命令无法连接该端口。缺少依赖时必须报告问题，不能静默切换到无沙箱执行。项目命令不会继承服务商凭据和控制令牌。

命令默认超时为 120 秒，可指定 1–3600 秒，但不得超过当前任务的截止时间。每个输出流上限为 256 KiB，发生截断时会明确报告。非零退出码会返回智能体，以便继续修复。命令应以前台方式运行，并清理自己启动的服务器。

一个正在执行或挂起的 Developer 任务会占用对应工作区，其他写入任务会收到 `WORKSPACE_BUSY`。写入、编辑和命令工具会在多个网关进程之间串行执行。开始另一个写入任务前，请先继续或取消挂起任务；同时避免在 Codex 或外部编辑器中修改相同文件。取消任务会停止命令进程组。已经产生的项目修改会保留供检查；llmTools 不会自动回滚、提交、推送或部署。

## 配置

完整示例见 [config.example.toml](config.example.toml)。最小 Pi 配置如下：

```toml
[general]
log_level = "info"
task_timeout_seconds = 1800
max_context_bytes = 1048576

[runtime]
type = "pi"
binary = "/absolute/path/to/pi"
session_dir = "/absolute/path/to/pi-sessions"

[agents.developer]
exposed = true
agent = ""
model = "provider/model"

[agents.review]
exposed = true
agent = ""
model = "provider/model"

[storage]
database = "llmtools.db"
```

模型 ID 应以 **Refresh Models（刷新模型）** 返回的结果为准，不要猜测模型名称。Pi 需要明确的 `provider/model` 路由，会话目录必须是规范化的绝对路径。Pi 配置不使用 `mode`、`host`、`base_url` 或 `port`；桌面端保存 Pi 设置时会清除旧版传输字段。

新建应用数据目录时默认使用 Pi，会话历史保存在该数据目录下的 `pi-sessions/`。macOS 默认数据目录为 `~/Library/Application Support/AI Worker`。已有的自定义 Pi 会话路径会保留。

### 限制项

- **Task timeout seconds（任务超时秒数）：** 每次委派或继续调用的时间上限，范围为 1–3600 秒，包含模型调用和工具执行。继续任务会获得新的截止时间；已经执行中的调用仍使用原截止时间。
- **Maximum task / continuation bytes（任务 / 继续请求最大字节数）：** 序列化后的传入请求大小上限，范围为 1024–4194304 字节。它限制提示词及补充数据的大小，不代表模型的完整上下文窗口，也不限制项目大小。
- **Codex MCP 工具超时：** 自动生成的配置使用 3615 秒，覆盖支持的最大任务超时和清理时间。

在存储和运行时标识未变化时，已有 MCP 进程会在下一次任务或继续调用时重新加载通用限制。切换运行时、更新可执行文件或修改角色暴露设置后，需要重新连接 MCP。

## 工作区授权

工作区授权是明确且持久保存的授权记录，不要求 Codex 提供 MCP Roots。

| 方式 | 操作 | 审计来源 |
| --- | --- | --- |
| 桌面端 | **Settings → Authorized Workspaces → Add Workspace（设置 → 已授权工作区 → 添加工作区）**，然后通过系统文件夹选择器选择目录 | `desktop_user_grant` |
| MCP | 调用 `ai_worker_add_workspace`，传入绝对路径和可选名称 | `mcp_client_grant` |

```json
{"path":"/absolute/path/to/project","label":"我的项目"}
```

两种方式创建的授权等效，都可以在桌面端查看和撤销。重复添加已经授权的规范化目录会返回原有授权。使用 `ai_worker_list_workspaces` 查询 ID；使用 `ai_worker_revoke_workspace` 并传入 `{"workspace_id":"ws_…"}` 撤销授权。

委派时通过 `workspace_id` 选择已有授权，不接受 `path`、`cwd` 或 `workspace_root`。Developer、Implementation 和 Review 必须绑定工作区。只有一个有效授权时可以自动选择；存在多个授权时必须指定 `workspace_id`。llmTools 不会猜测或静默替换工作区。Research 即使关联了授权，也仍然只能使用提供的上下文。

每次仓库操作都会重新检查当前授权和任务状态。撤销授权会取消绑定的任务，并阻止新的读取、写入和命令执行。工作区不可用时返回 `WORKSPACE_UNAVAILABLE`。名称仅用于展示，不改变权限。任务记录保存工作区标识和授权修订版本，不保存仓库文件内容或搜索结果。

## MCP 接口与会话

`delegate_developer` 参数示例：

```json
{
  "prompt": "在此项目中实现所需修改，运行相关测试并修复失败，报告修改的文件、实际执行的命令及结果，以及剩余问题。由 Codex 进行最终验收。",
  "workspace_id": "ws_…"
}
```

智能体收到的就是这段原始提示词及其已有的 Pi 会话历史，不会继承父级 Codex 的完整对话。请提供仓库中不存在的决策和上下文；有读取权限的角色可以自行定位项目文件。

任务完成后，MCP 文本就是智能体的原始回复。`structuredContent.content` 保存相同文本，`worker_run_id`、角色、模型和状态等生命周期元数据单独保存。待补充信息的请求和失败结果使用生命周期封装。运行时会话 ID、控制令牌和原始运行时事件不会作为公开 MCP 任务参数或完成文本暴露。

同一个 Codex 对话中的委派通过 Codex 的 `_meta.threadId` 共享一个持久 Pi 会话。每次委派仍会创建独立的任务记录，使用各自的路由和权限。会话历史可能包含同一对话中此前其他角色或工作区的委派内容。不同 Codex 对话使用不同的 Pi 会话；没有 `threadId` 的客户端会使用相互隔离的任务会话。

请按顺序提交委派。正在执行或等待上下文、决策的任务会占用当前对话会话，其他委派会收到 `SESSION_BUSY`。开始新任务前，应先继续或取消原任务。MCP 重新连接后会保留会话映射，但原 Pi 会话文件必须仍然存在。

### 等待补充信息

- `NEEDS_CONTEXT`：取得所需数据后，使用相同的 `worker_run_id` 和 `additional_context` 调用 `ai_worker_continue`。
- `NEEDS_DECISION`：由 Codex 作出决策，再使用相同 ID 和 `decision_responses` 调用 `ai_worker_continue`。
- 只有一项待回复请求时可以省略 `request_id`；有多项时必须使用返回的 `context_N` / `decision_N` ID。
- `ai_worker_cancel` 可以取消正在执行或挂起的任务，不会重试任务或修改路由。

继续任务会保留受支持的原 Pi 会话、模型、角色和工作区策略。历史或控制状态丢失时，会返回 `SESSION_LOST`、`PENDING_CONTEXT_LOST` 或 `PENDING_DECISION_LOST` 等稳定错误，llmTools 不会静默创建替代会话。

### 异常与恢复

智能体仍在执行时，可以继续修复编译或测试失败。达到任务级截止时间后，任务状态为 `FAILED`，错误码为 `TIMEOUT`。确认取消的任务为 `CANCELLED`；无法确认中止的任务为 `FAILED`，错误码为 `ABORT_UNCONFIRMED`。

进程重启时会检测遗留的执行中任务，尝试停止原执行并记录失败，不会自动重放提示词。只有 `NEEDS_CONTEXT` 和 `NEEDS_DECISION` 任务可以使用 `ai_worker_continue`；失败或已取消的任务不能原地重新打开。请先检查保留的项目修改，再重新委派。同一 Codex 对话中的新任务可以复用仍然存在的 Pi 历史。

### 打开 Pi 会话历史

实现检查见 [恢复命令验证记录](PI_RESUME_COMMAND_VALIDATION.md)。Runs 页面展示 **Role / model（角色 / 模型）**，不再重复展示 Agent。存在已验证 Pi 历史的任务，会在 **Run / objective（任务 / 目标）** 单元格显示命令和 **Copy pi --resume（复制 pi --resume）** 按钮。复制的命令通过 `--session` 指定准确的会话文件，可以直接打开该任务的对话；复制操作不会执行命令。路径会进行 shell 引号处理。新任务保存原始可执行文件、会话目录和会话文件元数据。旧 Pi 任务在当前会话目录中存在匹配历史文件时，也可以解析出恢复命令。历史文件缺失或旧 OpenCode 任务不会显示可复制的 Pi 命令。

会话保存在配置的 `runtime.session_dir` 中。手动打开会话选择器时，请指定该目录：

```sh
pi --session-dir "/absolute/path/to/pi-sessions" --resume
```

也可以使用 `pi --session "/absolute/path/to/session.jsonl"` 打开指定 JSONL 文件。Worker 任务 ID 不是 Pi 会话 ID。同一 Codex 对话中的多个任务可能共享会话选择器中的同一条记录。桌面端任务历史和日志与 Pi 自身的会话选择器相互独立。

## Codex 集成与 Skill 管理

桌面端 **Codex Integration（Codex 集成）** 标签页显示运行时协议、MCP 工具、角色暴露、工作区策略和 Skill 状态。可以注入、更新 / 重新注入或移除用户级 Skill，路径为 `~/.agents/skills/llmtools-delegation`。移除时需要在应用确认对话框中确认，仅影响这一受管理的 Skill 目录，不影响 MCP 配置、服务商凭据和会话历史。

Skill 当前版本为 **1.4.0**，指纹为 **llmtools-delegation-v5**。支持隐式选择，也支持通过 `$llmtools-delegation` 显式使用。仓库中的标准来源为 [SKILL.md](integrations/codex/plugins/llmtools/skills/llmtools-delegation/SKILL.md)。Skill 指导角色选择和同一任务的继续流程，不授予额外能力，最终验收仍由 Codex 负责，也不保证每个任务都会被委派。

本地插件包也包含该 Skill 和一个默认禁用的可选 MCP 定义：

```sh
codex plugin marketplace add /absolute/path/to/llmTools/integrations/codex
codex plugin add llmtools@llmtools-local
```

请只保留一份指向实际运行二进制的 MCP 注册。插件启动器接受 `LLMTOOLS_MCP_BINARY` 和 `LLMTOOLS_CONFIG_DIR`，不会安装 Pi 服务商或选择模型。

## 持续集成

[GitHub Actions CI](https://github.com/lazyun/llmtools/actions/workflows/ci.yml) 在推送到 `main`、所有 Pull Request 和手动触发时运行，使用 Go 1.26.x、Node.js 22.x 和 Python 3.13。

- **质量检查（macOS 15）：** MCP 模块的 Go vet 和竞态测试、Python 脚本测试、Codex 插件校验、前端构建与测试，以及桌面端模块的 vet 和测试。macOS runner 同时覆盖 Developer 命令沙箱。测试使用本地测试替身，无需安装 Pi、配置服务商凭据或调用真实模型。
- **原生构建：** macOS 15、Ubuntu 24.04 和 Windows Server 2022 调用现有平台脚本，并启用 CGO。Linux 安装 GTK 3 / WebKitGTK 4.1，Windows 在 Git Bash 中使用原生 Go 和 MinGW GCC。目前 Linux / Windows 验证编译与打包，完整测试套件在 macOS 上执行。
- **下载构建产物：** 构建成功后，可在该次运行的 **Artifacts** 区域下载 `macos-desktop-build`、`linux-desktop-build` 或 `windows-desktop-build`。其中包含 `.tar.gz`（macOS / Linux）或 `.zip`（Windows）归档，内含应用、MCP 和 MIT 许可证。Tar 归档保留可执行权限及 macOS 应用包签名。产物保留 7 天，CI 不会自动发布 Release。

工作流只读访问仓库，Actions 固定到已核对的提交 SHA，同一 ref 的新运行会取消旧运行，并基于两份 Go 模块校验文件和前端锁文件缓存 Go / npm 依赖。配置见 [ci.yml](.github/workflows/ci.yml)。

## 编译与验证

请在目标操作系统上运行对应脚本。完整构建会重新生成 Wails 绑定、构建前端、**启用 CGO** 编译使用 SQLite 的 MCP、构建桌面应用，并将两者一起打包。脚本会自行定位仓库，因此也可以从其他目录调用。

| 编译平台 | 命令 | 打包输出 |
| --- | --- | --- |
| macOS | `bash scripts/build-macos.sh` | `dist/macos-<arch>/AI Worker.app`，MCP 位于包内的 `Contents/MacOS` |
| Linux | `bash scripts/build-linux.sh` | `dist/linux-<arch>/AI Worker` 和 `ai-worker-mcp` |
| Windows，使用 Git Bash / MSYS2 | `bash scripts/build-windows.sh` | `dist/windows-<arch>/AI Worker.exe` 和 `ai-worker-mcp.exe` |

脚本使用本机 Go 宿主架构（`amd64` 或 `arm64`）进行原生编译，不提供 CGO 交叉编译封装。Windows 应在 Git Bash / MSYS2 中使用原生 Windows Go；WSL Go 生成的是 Linux 程序，Windows 脚本会拒绝它。分发 Linux / Windows 应用时，请将桌面可执行文件和 MCP 辅助可执行文件放在一起。macOS 应用包会进行临时签名并验证，适合本地使用；公证和 Developer ID 分发需要另行处理。

完整桌面构建的依赖：

- Go **1.26+**、Node.js **22.19+** 及 npm，以及可用的本机 C 编译器。SQLite 需要 CGO；禁用 CGO 的源码编译检查不能生成可用的发布版 MCP。
- 与 `desktop/go.mod` 一致的 Wails CLI，目前为 **v2.10.2**：`go install github.com/wailsapp/wails/v2/cmd/wails@v2.10.2`。脚本先检查 PATH，再检查首个 GOPATH 的 `bin` 目录；也可以设置 `WAILS=/absolute/path/to/wails`。
- macOS：Xcode Command Line Tools（`xcode-select --install`）、macOS 签名工具，以及包含 UniformTypeIdentifiers 的较新 SDK。
- Linux：C/C++ 编译工具链、`pkg-config`、GTK 3 和 WebKitGTK 开发包。在使用 WebKitGTK 4.1 的 Debian / Ubuntu 上，通常需要 `build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`。脚本检测到 4.1 时选择 `webkit2_41` 构建标签，否则检查 4.0。
- Windows：原生 Windows Go、PATH 中兼容的 MinGW-w64 GCC 工具链，以及 Git Bash 或 MSYS2 Bash。Wails 使用 WebView2 下载策略；用户电脑需已安装 WebView2，或能够联网使用引导安装程序。

```sh
# 复用已安装的前端 node_modules：
bash scripts/build-macos.sh --skip-install

# 只编译启用 CGO 的 MCP，无需 Node.js、Wails 和图形界面 SDK：
bash scripts/build-linux.sh --mcp-only

# 查看参数，不执行编译：
bash scripts/build-windows.sh --help
```

默认使用 `npm ci` 安装前端依赖；相关依赖和所需 Go 模块必须已经缓存或能够下载。脚本不会安装全局工具链、修改运行时设置或启动 Pi。`make desktop` 自动选择本机平台脚本；也可以使用 `make desktop-macos`、`make desktop-linux` 或 `make desktop-windows` 明确选择。

跨平台打包不会扩大运行时权限：Developer 的沙箱命令执行目前仍要求 macOS。在 Linux / Windows 上会返回 `EXECUTION_SANDBOX_UNAVAILABLE`，脚本不会绕过沙箱。

验证命令：

```sh
npm --prefix desktop/frontend test
go test ./...
python3 -m unittest discover -s scripts -p 'test_*.py'
```

替换可执行文件后，请重启 AI Worker，并重新连接 Codex MCP。

默认测试使用无需服务商的 Pi RPC 测试替身和本地项目。集成测试会启动实际生产 MCP，检查 Pi 路由、进程重启后的继续执行、原始提示词保留、固定模型、取消、限制热加载和工作区撤销。旧适配器的单元测试仅作为内部兼容性验证，不会在生产工厂或可执行文件中启用 OpenCode。真实服务商的 Pi 检查需要显式启用，其结果应与模拟测试分开报告。

带日期的验证证据见 [仅支持 Pi 的运行时验证](PI_ONLY_RUNTIME_VALIDATION.md)、[Developer 角色验证](DEVELOPER_ROLE_VALIDATION.md) 和 [Pi 行为一致性验证](PI_RUNTIME_PARITY_ACCEPTANCE_VALIDATION.md)。

## 许可证

本项目采用 [MIT 许可证](LICENSE)。第三方依赖仍适用各自的许可证。
