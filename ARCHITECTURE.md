# Lodestar 架构说明

本文描述 Lodestar 的运行时结构：两张图怎么组织、一条意图怎么从生成走到写回、四个智能体各自扮演什么角色，以及哪些边界是硬约束。

面向改造本项目的人。上游 Lodestar 的能力细节见 [`README.Lodestar.md`](./README.Lodestar.md)，增量改造清单见 [`CHANGELOG.LODESTAR.md`](./CHANGELOG.LODESTAR.md)。

---

## 1. 两张图

系统的全部状态落在 PostgreSQL 的两张互不重叠的图上。

### 1.1 资产图（`assets`）

回答"**已知世界长什么样**"。全局共享，跨任务复用，不是某个任务私有的。

节点类型由 `assets.type` 的 CHECK 约束限定为六种，并各自带唯一索引防止重复登记：

| type | 唯一性键 | 说明 |
|---|---|---|
| `root_domain` | `domain` | 根域名 |
| `subdomain` | `(domain, record_type)` | 子域名及其解析记录类型 |
| `ip` | `ip` | IP 地址，含 `bound_domains` / `open_ports` |
| `app` | `bundle_id`（或 `app_name`） | 应用/站点，含指纹 |
| `service` | http 按 `url`；其他按 `(domain, ip, port, service_name)` | 服务 |
| `endpoint` | `(url, method)` | 接口，含 `params` |

**关系是隐式的**：资产之间没有独立的边表，靠字段承载父子与归属关系——`root_domain` 字段把子域/服务/接口挂回根域，`bound_domains` 把 IP 与域名互连，`bundle_id` 把应用下的资产聚合。这比一等公民的图更简单，代价是遍历要靠索引查询而非图遍历。

资产在任务间的归属通过 `task_asset_links` 记录，并带**来源溯源**（`source` / `source_summary` / `source_node_id`）——某个资产是谁、因为哪条探索节点被纳入本任务的，可追溯。`assets.task_ids` 更新时由数据库触发器自动同步这张关联表。

### 1.2 探索图（`exploration_nodes` + `exploration_edges`）

回答"**这次任务推理到哪一步了**"。每个任务独占一张，任务删除时级联清除。

节点 kind 是一个闭合集，state 机按 kind 分别约束（数据库双 CHECK 强制）：

| kind | 允许的 state | 语义 |
|---|---|---|
| `goal` | `open` / `met` / `abandoned` | 任务目标 |
| `intent` | `open` / `running` / `paused` / `done` / `blocked` / `exhausted` / `stopped` | 一条探索方向（工作单元） |
| `fact` | `confirmed` / `dismissed` / `origin` | 已确认的探索结论（含**否定结论**） |
| `finding` | `confirmed` / `dismissed` | 已确认的漏洞 |
| `hint` | `active` / `consumed` | 人类/主 agent 的战略提示 |
| `hypothesis` | `open` / `confirmed` / `refuted` | 态势级猜想（第二 brain 产出） |
| `begin` | `open` | **已废弃**：旧任务根节点，现由 `fact` + `state=origin` 取代 |

`intent` 的状态数是所有 kind 里最多的，因为它同时承载"排队中 / 执行中 / 被人暂停 / 撞步数上限 / 被规划者终止"这些不同结局。区分 `exhausted`（试过没做完）与 `blocked`（出错）与 `stopped`（被 kill）很关键——规划者对这三种的处理完全不同。

**边**只有四种关系：

| rel | 方向 | 含义 |
|---|---|---|
| `spawns` | origin fact → goal；旧 begin → intent | 从属诞生 |
| `derived_from` | fact / finding / hypothesis / intent → intent；fact → hypothesis | 这条意图/假设是从什么推出来的 |
| `yields` | intent → fact / finding | 这条意图产出了什么 |
| `proves` | fact / finding → goal / hypothesis | 什么证据证实了它 |

主边是 `derived_from` + `yields` 这条链：**意图因某些事实而生，产出新的事实**。整张探索图就是这条链不断延伸的结果。

### 1.3 两图如何连接

`exploration_anchors(node_id, asset_id)` —— 探索节点锚定到资产。这条连接让"某条事实说的是哪个资产"可查询，也让按资产反查发现（`idx_anchor_asset`）成为可能。

**注意两个 id 空间不互通**：探索节点 id 与资产 id 是独立序列，`node_detail` 拒绝接收资产 id（工具描述里写明了这点，因为这是模型的高频错误）。

---

## 2. 一次任务的生命周期

```
创建任务
  → 种子 origin fact（探索图根）
  → goals 拆解器把任务描述拆成若干 goal
  → 启动循环（见 §3）
  → 终态：所有 goal 达成 / 超时 / 失败
```

任务状态机：`running` ↔ `paused`，终态 `done` / `failed` / `timeout`。终态后不再领取新意图，但主 Agent 对话仍可继续（走任务的 LLM 配置链）。

**任务级超时**由 `first_run_at + timeout_seconds` 算出 deadline，到点进入**收尾（settling）**阶段：不再领新意图，让在跑的意图自行收尾，协调器等其 drain。收尾期被取消的意图标 `exhausted` 而非 `blocked`。

**并发**：任务数量受全局并发上限约束（可关，默认关）；单任务内的 worker 数默认 3，可在系统设置里改。因上限被挂起的任务标 `queued`，有空位自动启动。

---

## 3. 运行时循环

每个任务启动时注册若干个 goroutine，数量精确匹配启用的角色：

```
1 个 plannerLoop
N 个 workerLoop（N = 并发 worker 数，默认 3）
+1 hypothesizerLoop（仅 LODESTAR_BRAIN2_ENABLED）
+1 verifierLoop    （仅 LODESTAR_VERIFIER_ENABLED）
```

计数精确是为了让 WaitGroup 不多不少——关闭的角色不占协程，也不空转。

### 3.1 plannerLoop —— 图变即唤醒

```go
select {
case <-t.notify:      // 边沿触发
case <-heartbeat.C:   // 心跳兜底
}
```

唤醒源有两类：
- **边沿**：worker 完成意图、产生 finding、被人 kill、任务 resume、种子首轮
- **心跳**：兜底死锁 + 周期性复查飞行中的 worker

每次唤醒后重臂心跳（从"上次任意规划"开始计时），所以频繁的边沿触发会推迟心跳——这正确，因为心跳的目的是"静默过久时兜底"。

**debounce**：一轮规划开始前先排空一段窗口内累积的触发事件，把突发的一批变更合并成一轮。触发的具体内容（哪个意图完成了）会在提示词顶部点明，让规划者先看真正变的东西。

**守卫序列**（顺序重要）：LLM 未配置 → 已暂停 → 删除中 → 任务终态 → 收尾中。任一命中直接返回。

### 3.2 workerLoop —— 轮询 frontier

没有队列，没有信号——每个 worker 循环领一条意图：

```
claimNext: Frontier() 取 open 意图（priority DESC, id ASC）→ ClaimIntent 用 UPDATE ... WHERE state='open' 做原子领取
  有活 → runIntent（执行 + 收尾）
  没活 → 800ms 退避后重试
```

`ClaimIntent` 用条件更新而非 SELECT-then-UPDATE，多 worker 并发领取不会重复。领取时 `owner` 写入 worker 名，便于 UI 显示"谁在跑"。

### 3.3 hypothesizerLoop / verifierLoop —— 只听心跳

这两个角色**从不监听 `t.notify`**，只按心跳触发。理由相同：如果它们也响应图变事件，就会和 planner 抢同一个唤醒，产生不必要的并发竞争。它们的产物不需要实时性——假设晚一轮被看到没有损失。

- 第二 brain 心跳 = planner × 3（规划者 10 分钟下限 → 第二 brain 30 分钟）
- 验证者心跳 = planner

### 3.4 一次意图的执行（`runIntent`）

这是最复杂的一条路径，因为它要处理所有中途取消的形态：

1. 盖首次运行时间戳、算 deadline
2. 建 work 级子 context —— 让规划者的 `kill_work` 只停这一条
3. 包装 guard hooks —— 让 `steer_work` 能在下一次工具调用前注入纠偏
4. **选后端**：`pickBackend` → CLI 或 norma
5. 执行
6. `model_error` 退避重试
7. **CLI 失败回退**：若 primary 是 CLI 且可重试，用 norma 对同一意图重跑一次
8. 收尾分流：

| 情形 | 落库状态 |
|---|---|
| 正常完成 | `done` |
| 出错 | `blocked` |
| 撞步数上限 | `exhausted` |
| 运行超时 | `exhausted` |
| 任务暂停取消 | 回退 `open`（恢复时续跑 transcript，不从头） |
| 任务收尾取消 | `exhausted` |
| 任务终态取消 | `stopped` |
| 被规划者 kill | `stopped`（不写回结果） |
| 被用户暂停/取消 | `paused`（可恢复） |

**取消语义**用 `context.Cause` 区分：任务级 cancel 与 work 级 cancel 走不同分支。这个设计曾被写成 bug（`workCtx.Err()` 在 unregister 后恒为真），现在的判据是"`workCtx` 出错但 `ectx` 正常"才算被 kill。

### 3.5 上下文压缩与自我交接

长跑的执行者（尤其多阶段利用）必然撞上上下文上限。norma 的压缩是**有损**的：把对话头部摘要成一段文字、保留最近若干条尾巴。只存在于头部里的细节——某个 payload 的变形、某次失败的确切报错——会消失。

但探索图不随压缩丢失，也不随重启或换人丢失。所以**压缩前那一刻**，是把状态写进图回报最高的时刻。`agent/handover.go` 就做这件事：

```
越过阈值 85%  ──► 要求立刻 record_fact 写【交接】事实
                        │
                    压缩发生（lossy）
                        │
token 骤降到 60% 以下 ──► 要求先 list_facts 找回交接再继续
```

**接入点**是 `agentcore.Options.Deps.CallModel` / `CallModelSync`——每次模型请求的必经之路。选它而不是 `Compactor`，是因为后者无法注入：`agentcore` 内部硬编码 `compaction.New(*opts.Compaction, ...)`，`Options` 只接受 `*compaction.Config`，没有传入自定义 `Compactor` 实现的入口。

注入只修改**发出去的请求**，不动存储的历史，所以 transcript 仍是原始完整对话。

两个判定各有一个容易出错的地方：

- **压缩后判定用 token 降幅，不用文案匹配**。若去匹配 norma 的摘要前言字符串，上游改一个字就失效。正常对话只会增长，工作集被压掉才会出现骤降。
- **"已发过"标志只在注入确实能落地时才置位**。若那轮历史正好停在未应答的 `tool_use` 上，追加 user 消息会破坏 `tool_use`/`tool_result` 配对、请求直接 400。此时必须不置位、等下一轮——否则一次尝试就烧掉了整次机会，而压缩来临时再也不会要求交接。

阈值按 compaction 的公式手算（`window − reservedForSummary − buffer`，buffer 分档）。这是刻意的近似：没有导出的 `Pre` 钩子，fork SDK 比这个启发式更糟。代码内以 `ponytail:` 注释标注了漂移风险——如果 norma 改了阈值算法，这里需要同步。

四个角色都接了守卫，阈值随各自的 context window 变化。压缩摘要调用本身（`ProviderSummarizer`）直接调 provider、不经过 `Deps`，因此不会误触发。

---

## 4. 四个智能体

全部是同一套 agentcore 会话，差别在**工具集**与**提示词**。

### 工具集分层

```
readTool   —— 只读，不计数
writeTool  —— 写回，计入 WriteCounts
```

各角色拿到的集合：

| 角色 | 工具集 | 能否写图 |
|---|---|---|
| planner | 只读态势工具 + `add_intent` / `prove_goal` / `goal_met` / `report_finding` / `resolve_hypothesis` / `kill_work` / `steer_work` + 资产与范围查询（`list_companies` / `add_task_scope` / `list_untested_assets`） | 能 |
| worker | 只读（`list_facts` / `list_findings` / `node_detail`）+ 轨迹检索（`search_all_worker_traces` / `list_worker_traces` / `get_worker_trace`）+ 资产（`insert_assets` / `list_assets` / `list_companies` / `add_company_scope`）+ `record_fact` / `report_finding` | 能 |
| hypothesizer | 只读态势工具（`graph_overview` / `list_findings` / `list_facts` / `node_detail` / `get_worker_output` / `search_all_worker_traces` / `list_goals`）+ `record_hypothesis` | 仅假设 |
| verifier | `node_detail` / `list_facts` + `actool.DefaultTools()`（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash/Sleep，`EnableWebFetch` 另开）+ `record_verification` | 仅复核结论 |

hypothesizer 与 verifier 的工具集是刻意收窄的：**没有 `add_intent`**，它们不能派意图；hypothesizer 连 `prove_goal` / `kill_work` / `steer_work` 都没有——只观察、只推断。验证者虽然能跑 shell（复核需要重放 PoC），但提示词明确限定只重放只读部分。

### 提示词的四段结构

```
[A] 角色正文      —— 可编辑，存 agent_prompts，UI 可改
[B] 流量工具说明  —— 代码注入，仅开启流量捕获时存在
[C] 中间产物规约  —— 代码所有，不可编辑
[D] 不可信数据边界 —— 代码所有，不可编辑
```

[B]/[C]/[D] 在 `renderSystem` 之后追加，**编辑 DB 里的 [A] 无法把它们删掉**。这是有意的：用户能改角色设定，但不能改掉安全边界和产物落盘规则。

[D] 的存在理由：探索图是共享写面——worker 把目标输出抄进 fact，下一轮 planner 又把该 fact 注入自己的提示。没有这条边界，目标机器上的一段文字就能指挥编排器。

### 最小权限的落实

- `verifier` 的 `record_verification` **在构造时绑定 node id**，模型无法把结论指向别的节点
- `hypothesizer` 的父锚白名单只接受 fact/finding，不能拿 goal/intent 当假设依据
- 所有写入走 `AddNode` 单一入口，kind 白名单 + payload 非空校验

---

## 5. 写回路径

**只有一个写图入口**：`ExplorationStore.AddNode`。

```
工具调用 → ToolSet.recordOneFact / addFinding / recordHypothesis
        → ts.AddNode(kind, payload, priority, state, origin, anchors)
        → 校验 kind ∈ 闭合集、payload 非空
        → 事务：INSERT node + INSERT anchors
        → 工具层再补边（derived_from / yields）
```

这条单一路径的意义：校验、血缘、锚定、`notifyFinding`、写回计数全部统一，任何新增写入者（包括 CLI worker 和未来的智能体）都自动继承。

CLI worker 的写回尤其依赖这点——它解析出结构化 JSON 后，**不是自己写库**，而是把每一项喂回同一批 `CoreTool`：

```
CLI 输出的 {facts, findings, new_assets}
  → ToolSet 里的 record_fact / report_finding / insert_assets
  → 与进程内 worker 完全相同的写回路径
```

所以 CLI worker 不需要重新实现任何血缘或校验逻辑。

### 幂等与去重

- **意图去重**（`Intent Admission`）：创建意图前算指纹（归一化文本 + 排序后的 asset_ids），命中活跃意图则返回其 id，不新建。这是对"规划者换个措辞提同一件事"的硬拦。
- **资产去重**：靠 `assets` 上的唯一索引 + `ON CONFLICT DO UPDATE`，同一资产重复登记是合并而非新增。

---

## 6. 后端分派

```
pickBackend(norma) → cliWorker != nil ? cliWorker : norma
```

**当前是全局开关，不是按意图分派。** 代码注释明确：`Global-switch only for now — no per-intent routing until there is a reliable signal for it`。按需分派需要判断"这条意图是否需要进程内能力"，目前没有可靠信号，所以没做。

### 两个后端的能力差异

| 能力 | norma（进程内） | CLI（子进程） |
|---|---|---|
| 实时写回 | 是，工具直连数据库 | 否，批处理 JSON 回写 |
| 跨 worker 轨迹共享 | 是，`search_all_worker_traces` 可见 | **否**，内部步骤不落 activity |
| guard/intercept 拦截 | 是，每次工具调用都过 | **否**，子进程自身的 Bash 不受控 |
| 代理录制 | 是 | 是（env 强制注入） |
| 能力广度 | Lodestar 内置工具 | 各厂商完整 agentic 能力 |

后两项"否"是 CLI 后端的**刻意天花板**，写在代码注释里，并在 README 中标注为授权实验室场景的已知限制。

### 回退链

CLI 失败（坏 key / 代理 / 5xx / 持续 `model_error`）且本次运行未被取消/暂停/kill/收尾时，用 norma 对同一意图重跑一次。这把 CLI 的能力天花板降级为完整 Lodestar 能力，而非让意图死路。

norma-only 部署时 `primary != worker` 恒为 false，整段回退代码不执行——与改造前逐字节等价。

---

## 7. 硬约束清单

这些是代码层面强制、无法通过配置绕过的边界：

| 约束 | 落点 |
|---|---|
| 节点 kind 必须属于闭合集 | `AddNode` + 数据库 CHECK |
| 节点 state 必须符合其 kind 的取值 | 数据库 `ck_node_state` 双 CHECK |
| payload 不能为空 | `AddNode` |
| 边的 rel 只能是四种之一 | 数据库 CHECK |
| 边不能自环 | 数据库 `ck_edge_noself` |
| 探索节点 id 不能当资产 id 用 | `node_detail` 工具校验 |
| 未配置 LLM 时不执行 | planner/worker 循环守卫 |
| 暂停/删除/收尾/终态时不领新意图 | workerLoop 守卫序列 |
| 复核结论不能改 finding 的 kind/state | `SetFindingVerification` 的 SQL 限定 |
| 提示词的 [B][C][D] 段无法通过 UI 删除 | `renderSystem` 之后追加 |

---

## 8. 数据流全景

```
                    ┌──────────────┐
   人类/主 Agent ───│  hint / goal │
                    └──────┬───────┘
                           │
                    ┌──────▼───────┐        ┌──────────────┐
                    │   planner    │◄───────│ hypothesizer │
                    │  规划者       │        │  第二 brain   │（心跳）
                    └──────┬───────┘        └──────▲───────┘
                           │ add_intent            │ record_hypothesis
                    ┌──────▼───────┐               │
                    │   frontier   │        ┌──────┴───────┐
                    │  (open 意图) │        │  hypothesis  │
                    └──────┬───────┘        └──────▲───────┘
                           │ claim                 │ proves
                    ┌──────▼───────┐               │
                    │    worker    │───────────────┘
                    │   执行者      │  record_fact / report_finding
                    └──┬────────┬──┘
          insert_assets│        │
           ┌───────────▼──┐  ┌──▼──────────┐
           │   资产图      │  │   探索图     │◄─── verifier（心跳）
           │  (全局共享)   │  │ (任务独占)   │     record_verification
           └──────────────┘  └─────────────┘
                  ▲                  ▲
                  └── exploration_anchors ──┘
```

---

## 9. 值得注意的取舍

**文件图 vs 数据库图**：上游及部分同类项目用 JSON 文件 + 文件锁。这里用 PostgreSQL，代价是需要一个数据库实例，换来的是事务、级联、CHECK 约束、并发 CAS 和跨任务查询。对"多 worker 同时写同一张图"这个场景，数据库的原子性是不可替代的。

**显式边 vs 隐式关系**：探索图有显式的边表（需要表达有向的推理链），资产图用字段承载关系（资产间是结构从属，不是推理链）。两者刻意不同构。

**轮询 vs 队列**：worker 轮询 frontier 而非订阅队列。多一次 800ms 级延迟的代价，换来的是"意图状态以数据库为唯一真相"——worker 崩了重启后自然接上，不需要重建内存队列。

**两个只读角色共用一条心跳**：它们不响应图变事件，减少了与 planner 的唤醒竞争，代价是产物有最多一个心跳周期的延迟。对"提出假设"和"复核发现"这类非实时任务，这个延迟可以接受。

**交接阈值用启发式而非钩子**：compaction 没有导出的 `Pre` 钩子，无法"恰好在压缩前"插入。这里改为在每个模型请求上估算 token、按阈值提前行动。代价是阈值公式要与上游保持同步（已在代码内标注漂移风险），收益是不必 fork SDK。同样的取舍也用在"压缩后"的判定上——用 token 降幅而非匹配上游的摘要文案。
