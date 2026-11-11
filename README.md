# Lodestar

多脑协同的自主渗透测试智能体。用一张可追溯的推理图驱动探索，用独立角色互相校验。

> **仅供授权测试与研究使用。** 未经明确授权，不得对任何系统、网络、应用或数据进行安全测试、利用或访问。使用者自行承担全部法律与合规责任。

个人研究项目。基座来自上游项目，思路借鉴 Cairn，角色划分参考 sapper-v2，来源详见文末的「参考」。

## 它做什么

规划者（planner）不断把"下一步做什么"写成意图。执行者（worker）领取意图、执行，再把结论写回图。另外两个独立角色分别提出假设（态势研判者）和复核发现（验证者）。

这样一次渗透可以追溯、可以校验，中断后也能恢复。项目重点不在单个 agent 更聪明，而在角色之间互相检查。

## 增量能力

| 能力 | 解决的问题 | 关键实现 |
|---|---|---|
| 可换脑的后端 | 借用各厂商完整的 agentic 能力，同时保留进程内的实时能力 | `AgentBackend` 接口、`CLIWorker` 批处理回写、失败时回退到 norma |
| 可验证的写回 | 图谱被重复或垃圾节点污染 | 意图指纹去重、`AddNode` 统一校验、`deviations` 偏离记录 |
| 多脑协同 | 单个 brain 容易自欺，也容易漏掉跨线索的关联 | 态势研判者提出假设，验证者复核发现 |

三项能力默认关闭。不配置任何 `LODESTAR_*` 变量时，行为与上游基座一致。

双图的组织方式、意图的完整生命周期、各角色可用的工具和硬性边界，见 [ARCHITECTURE.md](./ARCHITECTURE.md)。

## 架构要点

### 可换脑的后端

- `AgentBackend` 接口抽自基座 worker 的 `Execute` 与 `ExecuteWithMessage` 签名。进程内的 `*Worker`（norma）和 `CLIWorker`（子进程加批处理回写）实现同一接口，执行路径与后端无关。
- `CLIWorker` 的流程：引擎向子进程注入该意图的图上下文，子进程完整运行，壳程序解析尾部的结构化结果，再经现有 `ToolSet` 写回双图。所有写图都走同一条路径，因此校验、血缘和 `notifyFinding` 对两种后端都生效。
- 当前由 `pickBackend` 做全局开关：配置了 CLI 就全部走 CLI，否则全部走 norma。**按意图分派尚未实现**，需要可靠的意图特征信号，留待后续。
- 失败回退：CLI 子进程出错（密钥错误、代理故障、5xx、持续的 model_error），且本次运行没有被取消、暂停、kill 或进入收尾，就对同一意图用进程内 norma 重跑一次。仅使用 norma 的部署不会触发此回退。

### 可验证的写回

- **意图去重（Intent Admission）**：对活跃意图（open/running）计算规范化文本指纹，忽略大小写、空格、标点、语序和重复词，并结合排序后的 `asset_ids`。命中时不新建意图，返回已有意图的 id，且不改动其血缘。规划者的提示词只是软约束，这里做硬拦截，挡住"换个说法就重复"的意图。
- **`AddNode` 写回校验**：唯一的写入入口。写库前检查 kind 是否属于闭合集合、payload 是否非空。这是给绕过工具直接写图的新写入方预留的最后防线。
- **`deviations` 字段**：`record_fact` 可选记录执行时偏离原计划的地方，并回传给规划者。规划者看到的是意图实际怎么跑偏的，而不只是结果。

### 多脑协同

| 角色 | 职责 | 唤醒方式 |
|---|---|---|
| 规划者 planner | 读态势、判目标、补意图 | 图变即唤醒，另有心跳兜底 |
| 执行者 worker | 领一条意图、执行、写回 | 轮询 frontier，无活时 800ms 退避 |
| **态势研判者 hypothesizer** | 横向综合已确认事实，提出跨资产、跨链路的**假设** | 仅心跳（规划者间隔的 3 倍），不抢图变事件 |
| **验证者 verifier** | 在独立会话中只读地重放 PoC，复核发现 | 仅心跳 |

- **第二个 brain（态势研判者）**：产出 `hypothesis` 节点，状态流转为 `open → confirmed | refuted`。规划者消费 open 假设并派出验证意图，事实产出后用 `resolve_hypothesis` 定案。它去掉了规划者专属的机制（todolist、kill、steer、目标判定），只读，外加 `record_hypothesis`，且从不接入 notify，因此不会与规划者争抢唤醒。
- **验证者**：`report_finding` 原先完全依赖 worker 自述结论，而 worker 最容易把版本号或 CVE 命中当成漏洞。验证者在独立会话中只读地重放 PoC 的非破坏部分，结论写回 finding 节点。
  - 无法复现不等于已证伪。目标不可达时记为 `verified=false`，并注明"目标不可达，待复测"。
  - 结论只写入 `payload.verification`，不改 `state`。复核失败是给运营者的证据，不会自动关闭一个可能真实的漏洞。
  - 未复核（nil）与复核未通过（`verified=false`）在界面和 API 上严格区分。

### 安全边界

- **提示注入边界**：探索图是共享写面。worker 会把目标的 HTTP 响应、banner、页面里的"管理员留言"写进 fact，下一轮规划者又把这些 fact 注入自己的提示，形成一条从目标直达编排器的注入通道。为此新增一段由代码持有、不可编辑的提示段，声明图谱内容和目标输出都是数据，不是指令，并给出处置动作。改数据库里的提示词正文无法删除它。
- **验证者只读**：验证者不做写入、删除或修改口令之类的操作。

## 配置开关

全部默认关闭。不配置时，行为与上游基座一致。

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `LODESTAR_CLI_ENABLED` | 关 | 用 CLI 子进程作 worker 后端，否则全走 norma |
| `LODESTAR_CLI_COMMAND` | — | CLI 可执行文件，如 `claude` |
| `LODESTAR_CLI_ARGS` | — | 参数模板，逗号分隔。用字面量 `{{PROMPT}}` 传提示词，否则走 stdin |
| `LODESTAR_CLI_ENV` | — | 传给子进程的额外环境变量 |
| `LODESTAR_BRAIN2_ENABLED` | 关 | 启用态势研判者（第二个 brain） |
| `LODESTAR_BRAIN2_MODEL` | 继承 planner | 覆盖第二个 brain 的模型 |
| `LODESTAR_VERIFIER_ENABLED` | 关 | 启用发现复核 |
| `LODESTAR_VERIFIER_MODEL` | 继承全局 | 覆盖验证者的模型 |

## 快速开始

环境要求：

- Go，版本见 `go.mod`（当前为 1.26.3）
- Node.js，用于运行前端（Next.js 16）

开发模式，后端、流量代理和前端一起启动：

```bash
cd web && npm install && cd ..   # 首次运行需要安装前端依赖
./dev.sh                         # 后端 :8787，流量代理 :8788，前端 :3000
```

打开 **http://localhost:3000**。`dev.sh` 启动的后端不内嵌前端，直接访问 `:8787` 根路径会返回 404，这是正常现象。

首次进入 `/setup` 设置管理员密码。

编译单二进制（前端内嵌）运行 `./build.sh`。它先构建前端，再以 `-tags embedui` 编译 `./cmd/lodestar`。

启动参数：

| 参数 | 默认 | 作用 |
|---|---|---|
| `-addr` | `:8787` | HTTP 监听地址 |
| `-proxy` | `:8788` | 流量录制代理地址，留空则关闭 |
| `-data` | 可执行文件旁的 `data/` | SQLite 数据目录 |

测试的最小验证命令：

```bash
go build ./...
go test ./server -run TestPlannerIdleGate -count=1
```

## 已知限制

- **按意图分派后端未实现。** 目前是全局开关，要么全部走 CLI，要么全部走 norma。
- **CLI 后端的可见性不完整。** 子进程内部的步骤不写入 activity，`search_all_worker_traces` 看不到这些步骤。子进程自带的 Bash 也不受 guard 和 intercept 拦截。这些限制只适用于授权实验室场景。
- **输入 token 计数问题已修复并经线上验证。** 智谱等 Anthropic 兼容端点把真实的 input 放在 `message_delta` 中，norma 原先只从 `message_start` 计数，导致 input 为 0。修复见提交 `432af24`。2026-11-11 实测任务（Task 1「空转门控实测」）的 `usage`/`result` 行中 `input_tokens`（4211、11703）与 `cache_read_tokens`（576）均正常非零落库。根因和修复记录见 [`CHANGELOG.LODESTAR.md`](./CHANGELOG.LODESTAR.md)。
- **空转门控已端到端验证。** 心跳在图无变化时不规划，等满两个窗口（60 分钟）才规划一次。2026-11-11 实测：连续两个周期内，10 分钟心跳全部被门控跳过（零 LLM 调用），规划严格在距上轮结束 60 分钟（60m 01s）时触发，且触发后计数器正确重置。

## 仓库结构

| 路径 | 内容 |
|---|---|
| `cmd/lodestar` | 程序入口 |
| `server/` | HTTP 接口、引擎、规划者与执行者循环 |
| `db/` | 探索图、资产图与 activity 的存储 |
| `llmrec/`、`llmpool/` | LLM 调用记录与连接池 |
| `third_party/norma` | norma SDK 的本地 fork，`go.mod` 用 `replace` 指向这里 |
| `web/` | Next.js 前端 |
| `ARCHITECTURE.md` | 架构与边界说明 |

## 变更日志

- [`CHANGELOG.LODESTAR.md`](./CHANGELOG.LODESTAR.md)：本项目的增量改造（阶段 1 至 7）

## 许可

- 依赖 [norma](https://github.com/Autumn-27/norma) SDK。本地使用的是 `third_party/norma` 中的 fork，`go.mod` 用 `replace` 指向这里。
- 仓库根目录没有 LICENSE 文件。`web/LICENSE` 是 MIT 许可（Copyright (c) 2024 Mohammed Arham Khan），只适用于 `web/` 目录。其余部分的许可状况不明确，再分发或商用前需要先确认授权。

## 参考

本项目参考或基于以下项目。

| 项目 | 在本项目中的作用 | 链接 |
|---|---|---|
| ARTEX（上游基座） | 项目骨架：双图架构（资产图全局共享、探索图每个任务独占）、事件驱动引擎、进程内 norma harness、Web UI、流量录制、审批门。本项目 vendor 了其源码，module path 原为 `github.com/Autumn-27/artex`，现已改为 `github.com/wkend/lodestar`。 | 原仓库 `github.com/Autumn-27/ARTEX` 已不可访问 |
| Cairn | 把通用 agent CLI 当 worker 使用的可换脑思路，以及批处理式的结论回写 | [oritera/Cairn](https://github.com/oritera/Cairn) |
| sapper-v2 | 独立复核者角色，以及提示词中的不可信数据边界、执行偏离记录 | [N1nEmAn/sapper-v2](https://github.com/N1nEmAn/sapper-v2) |
| norma | 进程内的 agent SDK，本项目的执行后端 | [Autumn-27/norma](https://github.com/Autumn-27/norma) |

上游 ARTEX 的许可状况不明确（见上文「许可」）。
