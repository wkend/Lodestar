# Lodestar

**基于双图推理架构与多角色协同校验的自主渗透测试系统**

> **合规与免责声明**：本系统仅用于经明确书面授权的安全测试、漏洞验证与学术研究。严禁对任何未获授权的系统、网络、应用或数据发起探测、利用或访问。使用者须自行承担因使用本软件产生的全部法律与合规责任。

---

## 1. 项目概述

**Lodestar** 将单次渗透测试建模为**可追溯、可校验、可中断恢复**的多智能体协作过程。系统以两张持久化图谱为核心状态面：

- **资产图（Asset Graph，全局共享）**：跨任务沉淀目标企业的域名、IP、端口、服务指纹与归属关系，支持 ScopeSentry 资产同步与覆盖度统计。
- **探索图（Exploration Graph，任务独占）**：有向无环推理图，包含五类闭合节点（`fact`、`intent`、`finding`、`goal`、`hypothesis`）及血缘边（`derived_from`、`proved_by` 等），完整记录每一次探测的依据、偏离与证据链。

在此基座之上，Lodestar 引入了**可插拔执行后端（`AgentBackend`）**、**写回准入与偏离审计**、**多脑协同（态势研判者 + 独立验证者）**以及**不可信数据边界与上下文自交接机制**。详细架构设计与不变量定义请参阅 [`ARCHITECTURE.md`](./ARCHITECTURE.md)。

---

## 2. 核心架构与技术特性

### 2.1 四角色多脑协同

系统将自主渗透拆分为四个职责隔离、工具集受限的角色，消除单智能体架构常见的“自说自话”与“未经复现即误报”问题：

| 角色 | 源码实现 | 核心职责 | 唤醒与调度机制 |
|---|---|---|---|
| **规划者（Planner）** | `agent/planner.go` | 读取探索图态势、判定目标达成状态、拆解并派发下一步意图（`intent`） | 事件边沿触发（`notify`）+ 周期心跳兜底（带 60 分钟空转门控 `plannerIdleGate`） |
| **执行者（Worker）** | `agent/worker.go` / `agent/cli_worker.go` | 认领开放意图、执行探测与验证、通过 `ToolSet` 将事实/资产/漏洞写回双图 | 轮询任务 Frontier（空闲时 800ms 退避），支持多实例并发 |
| **态势研判者（Hypothesizer）** | `agent/hypothesizer.go` | 横向综合已确认事实，提出跨资产、跨链路的攻击假设（`hypothesis`） | 仅心跳驱动（间隔为规划者的 3 倍），不监听 `notify`，零唤醒竞争 |
| **独立验证者（Verifier）** | `agent/verifier.go` | 在独立会话中以**只读**方式重放 PoC 关键证据，对 `finding` 节点给出复核结论 | 仅心跳驱动（每轮上限 `verifierBatch = 3`），支持同 Provider 瞬时故障退避重试 |

**关键校验语义**：
- **验证者只读与非证伪原则**：验证者仅执行非破坏性验证（如 GET/HEAD 请求、只读查询、检查已落盘证据）。复核结论写入 `payload.verification`（`verified: true | false` 及具体 `issues`），**不修改 `finding` 节点的 `state`**；目标不可达或需写操作而未重放时标记为 `verified=false` 并写明原因，不将“无法复现”等同于“漏洞已证伪”。
- **假设闭环状态机**：`hypothesis` 节点遵循 `open → confirmed | refuted` 状态流转；规划者消费 `open` 假设并派生验证意图，获得事实证据后通过 `resolve_hypothesis` 定案。

### 2.2 可插拔执行后端（`AgentBackend`）

引擎通过 `agent/backend.go` 定义的 `AgentBackend` 接口驱动执行者，支持两种后端无缝切换与故障回退：

1. **进程内 `norma` 后端（`*Worker`）**：基于内置 `norma` SDK 直接驱动模型工具循环，完整记录 `activity` 轨迹，受 `guard` 与 `intercept` 实时拦截保护。
2. **外部 CLI 委派后端（`*CLIWorker`）**：将单条意图定向切片注入外部通用智能体 CLI 子进程（如 `claude`、`codex`），子进程完成后解析尾部结构化 JSON 并统一经本进程 `ToolSet` 写回数据库，复用全部血缘校验与触发器。
3. **自动降级回退**：当启用 `LODESTAR_CLI_ENABLED` 且 CLI 子进程因环境、代理或模型错误失败（且任务未处于取消/暂停/收尾状态）时，引擎自动回退至进程内 `norma` 后端重跑该意图。

### 2.3 写回准入、上下文交接与安全边界

- **意图去重（Intent Admission，`agent/tools.go`）**：对处于 `open`/`running` 状态的活跃意图计算规范化文本指纹（忽略大小写、空白、标点、词序与重复词）并结合排序后的 `asset_ids`。命中重复时直接返回已有意图 ID，从工具层硬性拦截重复派单。
- **节点写入强校验（`db/exploration.go` `AddNode`）**：所有图节点入库前强制校验 `kind` 属于合法闭合集合且 `payload` 非空。
- **执行偏离记录（`deviations`）**：`record_fact` 支持记录 `{planned, actual, reason}` 偏离项并透出至规划者视图（`recent_facts`），使规划者能够感知执行过程与原计划的实际偏差。
- **不可信数据提示注入边界（`agent/untrusted.go`）**：在规划者、执行者、研判者与验证者提示词中强制挂载不可经数据库编辑移除的边界声明，明确目标响应与图谱内容均为待分析数据而非指令。
- **上下文压缩前自我交接（`agent/handover.go`）**：在模型上下文占用达到压缩阈值的 85% 时注入交接指令，促使智能体将已确认结论、未完事项与死路写入探索图；压缩完成后检测到输入 Token 骤降（低于前次 60%）时自动提示先检索交接事实再继续执行。

---

## 3. 环境要求与快速开始

### 3.1 基础依赖

- **Go**：`1.26.3` 或更高版本（见 `go.mod`）
- **Node.js**：`20+`（用于构建或运行 `web/` 目录下的 Next.js 16 前端）
- **PostgreSQL**：`16+`（核心存储：探索图、资产图、活动轨迹、LLM 记录与系统设置）

### 3.2 数据库与配置初始化

PostgreSQL 连接信息按以下优先级解析：**环境变量 `LODESTAR_PG_DSN` > 配置文件 `config.json`**。

```bash
# 方式 A：通过环境变量指定数据库连接
export LODESTAR_PG_DSN="postgres://lodestar:yourpass@127.0.0.1:5433/lodestar?sslmode=disable"

# 方式 B：复制配置模板并填写 database 字段
cp config.example.json config.json
```

### 3.3 启动运行

#### 方式一：源码开发模式（前后端分离热更新）

```bash
# 1. 首次运行需安装前端依赖
cd web && npm install && cd ..

# 2. 同时启动 Go 后端(:8787)、流量录制代理(:8788) 与 Next.js 前端(:5173)
./dev.sh
```

启动后访问 **`http://localhost:5173`**（开发模式下前端通过 `/api/*` 自动反代至 `:8787`；直接访问 `:8787` 根路径返回 `404` 属正常现象）。首次访问将自动跳转至 `/setup` 初始化管理员密码（固定管理员账户名为 `Lodestar`）。

#### 方式二：编译单二进制生产包（内嵌静态前端）

```bash
./build.sh
./dist/lodestar-$(go env GOOS)-$(go env GOARCH)/lodestar -addr :8787 -proxy :8788
```

`build.sh` 会先执行 `web/` 静态导出，再以 `-tags embedui` 编译单二进制文件。启动后直接访问 **`http://localhost:8787`**。

#### 方式三：Docker Compose 部署

```bash
cp .env.example .env   # 编辑 .env 设置 POSTGRES_PASSWORD 与模型密钥
docker compose up -d
```

---

## 4. 配置参数与环境变量

### 4.1 命令行启动参数（`cmd/lodestar`）

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-addr` | `:8787` | 后端 HTTP API（及内嵌前端）监听地址 |
| `-proxy` | `:8788` | HTTP/HTTPS 流量录制 MITM 代理监听地址（传空字符串则禁用） |
| `-data` | `<BaseDir>/data` | 运行时文件数据目录（任务工作区、会话 Transcript、归档包等） |

### 4.2 环境变量一览

#### 基础与模型配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `LODESTAR_PG_DSN` | — | PostgreSQL 完整连接串（最高优先级，覆盖 `config.json`） |
| `LODESTAR_CONFIG` | `./config.json` | 显式指定配置文件路径 |
| `LODESTAR_SKILL_DIR` | `./skills` | 显式指定技能目录（覆盖 `config.json` 的 `skill_dir`） |
| `LODESTAR_LLM_PROVIDER` | 自动推断 | 全局默认模型供应商：`anthropic` 或 `openai`（Web UI 已配置默认 Profile 时以数据库为准） |
| `LODESTAR_LLM_MODEL` | 依供应商而定 | 全局默认模型 ID |
| `LODESTAR_LLM_BASE_URL` | 官方端点 | 模型 API 基础 URL（支持兼容端点） |
| `LODESTAR_LLM_PROXY` | — | 模型 API 请求代理（支持 `http`/`https`/`socks5`） |
| `LODESTAR_LLM_STREAM` | `true` | 是否默认启用流式响应（设为 `false`/`0`/`off` 走非流式） |
| `ANTHROPIC_API_KEY` | — | Anthropic 格式 API 密钥 |
| `OPENAI_API_KEY` | — | OpenAI 格式 API 密钥 |

#### Lodestar 多脑协同与换脑开关（默认全部关闭）

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `LODESTAR_CLI_ENABLED` | `false` | 启用外部 CLI 子进程作为主执行者后端（失败自动回退进程内 `norma`） |
| `LODESTAR_CLI_COMMAND` | — | 外部 CLI 可执行文件名或路径（例如 `claude`、`codex`） |
| `LODESTAR_CLI_ARGS` | — | 传给 CLI 的参数模板（逗号分隔；含 `{{PROMPT}}` 时替换提示词，否则经 `stdin` 传入） |
| `LODESTAR_CLI_ENV` | — | 附加给 CLI 子进程的环境变量列表（逗号分隔 `KEY=VALUE`） |
| `LODESTAR_BRAIN2_ENABLED` | `false` | 启用态势研判者（Hypothesizer / 第二 Brain） |
| `LODESTAR_BRAIN2_MODEL` | 继承规划者 | 覆盖态势研判者使用的模型 ID |
| `LODESTAR_VERIFIER_ENABLED` | `false` | 启用发现独立验证者（Verifier） |
| `LODESTAR_VERIFIER_MODEL` | 继承全局配置 | 覆盖独立验证者使用的模型 ID |

---

## 5. 仓库目录结构

| 路径 | 说明 |
|---|---|
| `cmd/lodestar/` | 主程序入口、命令行参数解析与启动生命周期管理 |
| `server/` | HTTP API 路由、自主编排引擎（`engine.go`）、心跳空转门控（`planner_idle.go`）、同源退避重试（`provider_retry.go`）及鉴权管理 |
| `agent/` | 四类智能体实现（Planner / Worker / CLIWorker / Hypothesizer / Verifier）、工具集定义（`tools.go`）、不可信数据边界与上下文自交接（`handover.go`） |
| `db/` | PostgreSQL 存储层：探索图、资产图、任务范围、活动记录、漏洞发现与数据库迁移 |
| `guard/`、`intercept/` | 命令安全守卫（Hook 拦截）与高危操作人工审批门 |
| `traffic/` | 内置 MITM 流量录制代理与请求检索 |
| `llmpool/`、`llmrec/` | 多模型配置池、速率限制、故障转移与 LLM 原始报文录制 |
| `mcphttp/`、`skills/` | MCP 客户端适配与内置技能包（`SKILL.md`） |
| `report/`、`enrich/` | 漏洞报告生成与资产指纹富化 |
| `third_party/norma/` | 进程内 Agent SDK `norma` 的本地维护分支（经 `go.mod` `replace` 绑定） |
| `web/` | Next.js 16 + Tailwind CSS v4 前端控制台 |
| `ARCHITECTURE.md` | 双图结构、意图状态机、角色工具矩阵与硬性安全边界规范 |
| `CHANGELOG.LODESTAR.md` | Lodestar 各阶段架构演进、实弹验证记录与已知问题台账 |

---

## 6. 构建与测试验证

```bash
# 全量编译检查
go build ./...

# 静态分析与核心包单元测试
go vet ./server ./agent ./db ./cmd/lodestar
go test ./server ./agent ./cmd/lodestar -count=1
```

---

## 7. 已知限制与边界

1. **按意图粒度分派后端尚未实现**：当前 `pickBackend` 为全局开关（启用 `LODESTAR_CLI_ENABLED` 后所有意图均优先走 CLI 后端，失败回退 `norma`），尚未根据意图特征动态路由。
2. **CLI 后端的过程可见性与拦截边界**：外部 CLI 子进程的内部推理步骤不逐条写入 `activity` 表（`search_all_worker_traces` 仅对进程内 `norma` 轨迹生效），且子进程内部自带的 Shell 工具不受本进程 `guard`/`intercept` Hook 拦截，仅适用于受控授权靶场环境。
3. **技能清单长度上限**：`third_party/norma/skill/skill.go` 对单次注入的技能清单设有 `maxSkillListingChars = 8000` 字符预算（超出后降级为仅保留技能名称 `name-only`，不会丢弃技能）。当前内置技能仅占约 0.64k 字符，余量充足。

---

## 8. 许可说明与参考致谢

### 许可说明

- `web/` 前端目录源自开源模板的部分遵循 [`web/LICENSE`](./web/LICENSE)（MIT License）。
- 仓库其余部分包含源自上游基座的代码与本项目的增量改造；因上游基座与 `norma` SDK 原始发布时未附带开源许可证文件，本仓库仅作为个人安全研究与技术验证使用。任何公开再分发或商业使用前，须自行确认并取得相关权利人授权。

### 参考与致谢

| 项目 | 关系与贡献说明 | 链接 |
|---|---|---|
| **ARTEX** | **上游基座**：提供双图架构（资产图 + 探索图）、事件驱动引擎、进程内 `norma` Harness、流量录制代理与 Web 控制台基础框架。本项目在其源码基础上进行重命名与多脑协同架构演进。 | 原仓库 `github.com/Autumn-27/ARTEX`（已下线） |
| **Cairn** | **架构思想参考**：借鉴其将通用 Agent CLI 作为可替换执行后端（`AgentBackend`）及批处理结构化回写的思路（未复制其代码）。 | [oritera/Cairn](https://github.com/oritera/Cairn) |
| **sapper-v2** | **角色与纪律参考**：借鉴其独立复核者（Verifier）角色划分、不可信数据边界声明及执行偏离记录（`deviations`）设计（MIT 许可，按本项目架构重写）。 | [N1nEmAn/sapper-v2](https://github.com/N1nEmAn/sapper-v2) |
| **norma** | **进程内 Agent SDK**：提供模型流式调用、工具调度、上下文压缩与 Transcript 记录基础能力（仓库内维护于 `third_party/norma`）。 | [Autumn-27/norma](https://github.com/Autumn-27/norma) |
