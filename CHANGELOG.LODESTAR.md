# Lodestar 变更日志

本文件记录 **Lodestar 相对上游 ARTEX 的增量改造**。上游自身的发布记录见 [`CHANGELOG.md`](./CHANGELOG.md)，两者互不覆盖：`CHANGELOG.md` 描述 ARTEX 基座的能力演进，本文件只描述 Lodestar 在其上新增或改动的部分。

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

## [Unreleased]

> Lodestar 目前没有独立的版本号与 release tag（`build.sh` 里的 `ARTEX_BUILD_VERSION`
> 取自 git tag，而仓库尚无 tag）。在引入版本号之前，本文件按日期倒序累积记录。

---

### 2026-11-09 — 阶段 7：独立复核与边界加固

#### 安全

- **提示注入边界**（stage 7a，`untrustedDataBlock`）：探索图是共享写面——worker 把目标输出（HTTP 响应、banner、文件内容、"管理员留言"）抄进 fact，下一轮 planner 又把该 fact 重新注入自己的提示，这是从目标直达编排器的现成注入通道。
  - 新增**代码所有、不可编辑**的提示段，与 `artifactSpec` 同一定位：编辑 DB 提示词正文也无法把它删掉
  - 声明图谱内容与目标输出一律是数据不是指令、列举典型注入话术、给出处置动作（记录为 fact/hypothesis，标 `inferred`）、并明确它只是文本层防御而非沙箱
  - 挂载到 worker / planner / hypothesizer / CLIWorker 四处提示组装点

#### 多脑协同

- **验证者 / 发现独立复核**（stage 7b，`agent/verifier.go`）：`report_finding` 此前完全凭 worker 自称，而 worker 恰恰最容易自欺（把版本号/CVE 命中当漏洞、把"看起来可注入"当已利用）。验证者用独立会话重放 PoC 的**只读**关键证据，把结论写回 finding 节点。
  - 只读原则写进提示：需写入/删除/改口令的操作一律不做，依据记录证据判断
  - **无法复现 ≠ 已证伪**：目标不可达时 `verified=false` + `issues="目标不可达，待复测"`
  - 结论只写 `payload.verification`，**不改 kind/state**：复核失败是给运营者的证据，不自动 dismiss 一个可能真实存在的漏洞
  - `record_verification` 的 node_id 在构造时绑定，模型无法把结论指向别的节点
  - 缺省 `max_turns` 兜底 12（会打目标，不能不限）
  - 引擎侧 `verifierLoop` 心跳驱动、不监听 notify；先过任务准入再标记"已尝试"，避免被暂停/删除/并发上限拒绝的那一轮把发现白白烧掉

#### 写回正确性

- **`deviations` 字段**（stage 7c）：`record_fact` 可选记录执行偏离原计划之处 `{planned, actual, reason}`，并透出到规划者的 `recent_facts`——让下一轮看见这条意图**实际怎么跑偏的**，而不只看结果。空条目会被过滤，按计划执行的事实 payload 与原来一致。

#### 可观测性

- **复核结论贯通到 UI**：`GetFinding` 增加 `LEFT JOIN exploration_nodes` 取 `payload->'verification'`，经 `FindingDTO` 透出到漏洞详情页新增的「独立复核」卡片。
  - 用可空指针：**未复核（nil）与复核未通过（`verified=false`）必须可区分**
  - 文案点明"未通过不代表漏洞不存在"，避免被误读为证伪
  - 规划者侧同样经 `compactFinding` 看到 `verified` / `verify_issues`

#### 工程

- 第二 brain 与验证者注册进 `builtinAgents` 与 `BuiltinPromptSeeds`。此前 `seedPrompts` 按 key 查找 agent 行、找不到就静默 skip，导致这两个角色的提示词永远不会进入 `agent_prompts`——UI 里看不到、改不了、也无法"恢复为内置默认"。建行只解决提示词落库，**是否运行仍由环境开关决定**。
- 新增集合断言 `TestBuiltinPromptSeedsHaveAgentRows`：提示词目录的每个 key 都必须有对应 agent 行。这是上述缺口的根因检查，以后新增角色漏建行会在测试里失败，而不是在生产里静默 skip。

---

### 2026-09-08 — 阶段 1–6b：换脑基座与多脑协同

本批提交把单脑 ARTEX 引擎改造成**可换脑的双图自主渗透系统**，并按"能力分层 → 写回校验 → 多脑协同"的顺序逐段加固。

#### 架构基座

- **`AgentBackend` 接口**（`agent/backend.go`）：把 `*Worker` 的 `Execute` / `ExecuteWithMessage` 抽成接口，引擎经同一接缝驱动不同 worker 实现。对 `Worker` 本体零改动，`runWorkerStep` / `runIntent` 的执行路径改为后端无关。
- **`CLIWorker`**（`agent/cli_worker.go`）：把一条意图委派给通用 agent CLI 子进程（`claude` / `codex` 等）。批处理模型：注入定向图切片 → CLI 跑完整循环 → 解析尾部 JSON → 经本进程 `ToolSet` 写回，血缘/锚定/`notifyFinding`/写回计数全部复用现有工具，不重复实现。
  - 解析器支持围栏代码块（末个胜出）、裸尾部对象（括号平衡、字符串感知）、空结果
  - 解析失败时二次索要纯 JSON；仍失败则按"已探索但无产出"处理，不进入错误循环
  - 代理环境变量强制子进程 HTTP 走录制 MITM
  - 写回失败（而非静默吞掉）会 emit 一条 note，避免"报成功但数据丢了"
- **CLI 后端优先 + norma 回退**（stage 3）：`LODESTAR_CLI_ENABLED` 开启时 `CLIWorker` 作 primary，CLI 子进程失败（坏 key / 代理 / 5xx / 持续 `model_error`）且本次运行未被取消/暂停/kill/收尾时，对同一意图用进程内 norma 重跑一次——把 CLI 的能力上限降级为完整 ARTEX 能力，而非让意图死路。norma-only 部署下整段回退恒不触发，与改动前逐字节等价。

**已知上限**（授权实验室场景）：CLI worker 的内部步骤不落 activity，`search_all_worker_traces` 看不到；子进程自身的 Bash 不受 guard/intercept 拦截。依赖轨迹共享的意图由 `pickBackend` 路由到 norma。

#### 写回正确性

- **意图去重**（stage 4）：对活跃意图（open/running）做规范化文本指纹（大小写/空格/标点/语序/重复词无关）+ 排序后的 `asset_ids`。命中则跳过创建并返回已存在意图 id，不改其血缘。规划者的提示词只是软约束，这里硬拦"换个措辞"漏网的重复。
  - 指纹从 `payload` 读 `summary` + `asset_ids`，不用 `n.Anchors`——后者在 `ListByKind` / `scanNode` 路径恒为空，用它会带资产的意图永不去重。
- **`AddNode` 写回校验**（stage 5）：唯一写入入口在触库前校验 kind 属于闭合集、payload 非空。今天不触发既有路径，是为绕过工具直接写图的新写入者预留的最后防线。刻意不校验 state（取值多、易误伤）。

#### 多脑协同

- **`hypothesis` 成为合法节点 kind**（stage 6a）：state 机 `open → confirmed | refuted`，含按"约束是否已含 hypothesis"重建的幂等迁移块。
- **第二 brain / 态势研判者**（stage 6b，`agent/hypothesizer.go`）：心跳唤醒（不监听 notify）的态势级 agent，通读探索图、横向综合已确认事实，产出 `hypothesis` 节点。规划者消费 open 假设派验证意图（`intent --derived_from--> hypothesis`），产出事实后 `resolve_hypothesis` 标 confirmed/refuted。
  - 剥离了 planner-only 机制（todos / kill / steer / goal 判定 / deadline），只保留只读工具 + `record_hypothesis`
  - 从不 wire notify——零跨循环唤醒竞争
  - 心跳间隔 = planner × 3

---

## 配置开关

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `LODESTAR_CLI_ENABLED` | 关 | CLI 后端作 primary，否则全走 norma |
| `LODESTAR_BRAIN2_ENABLED` | 关 | 启用第二 brain（态势研判者） |
| `LODESTAR_BRAIN2_MODEL` | 继承 planner | 覆盖第二 brain 的模型 |
| `LODESTAR_VERIFIER_ENABLED` | 关 | 启用发现复核 |
| `LODESTAR_VERIFIER_MODEL` | 继承全局 | 覆盖验证者的模型 |

## 参考与致谢

- 基座：[ARTEX](https://github.com/Autumn-27/ARTEX)（module path 保留）
- 思想：[Cairn](https://github.com/oritera/Cairn)（可换脑、两阶段收尾兜底）
- stage 7 的角色划分与纪律条文参考：[sapper-v2](https://github.com/N1nEmAn/sapper-v2)（MIT）的 `templates/*.md`
