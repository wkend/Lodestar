# Lodestar

混合多脑的自主渗透测试智能体 —— 在双图上朝目标导航。

个人研究项目。基于 [ARTEX](https://github.com/Autumn-27/ARTEX) 二次开发,融合 [Cairn](https://github.com/oritera/Cairn) 的核心思想。

## 这是什么

Lodestar 把两个开源渗透 agent 的优点合到一起:

- **ARTEX 作基座**:双图架构(资产图 + 探索图)、事件驱动引擎、norma 进程内 harness、产品层(UI/流量录制/审批门)。
- **Cairn 作思想**:可换脑(把通用 agent CLI 当 worker)、两阶段收尾兜底、受控状态空间搜索。

在此之上做了三件事:**可换脑的后端**、**可验证的写回**、**多脑协同**。

## 架构要点

### 可换脑的后端

- **`AgentBackend` 接口**:抽自 ARTEX 现有 worker `Execute` / `ExecuteWithMessage` 签名。`*Worker`(进程内 norma)与 `CLIWorker`(子进程委派 + 批处理写回)对引擎同一接口,执行路径后端无关。
- **`CLIWorker` 批处理契约**:壳注入定向图上下文 → CLI 跑完整循环 → 解析尾部结构化结果经现有 `ToolSet` 写回双图。写图单一路径,校验/血缘/`notifyFinding` 全自动统一。
- **`pickBackend` 目前是全局开关**:配了 CLI 就走 CLI,否则走 norma。**尚未做按 intent 的分派**——那需要可靠的意图特征信号,留待后续。
- **失败回退**:CLI 子进程出错(坏 key / 代理 / 5xx / 持续 model_error)且本次运行未被取消/暂停/kill/收尾时,对同一意图用进程内 norma 重跑一次,把 CLI 的能力上限降级为完整 ARTEX 能力,而非让意图死路。norma-only 部署下这段回退恒不触发。

### 可验证的写回

- **Intent Admission 去重**:活跃意图(open/running)做规范化文本指纹(大小写/空格/标点/语序/重复词无关)+ 排序后的 `asset_ids`。命中则跳过创建并返回已存在意图 id,不改其血缘。规划者的提示词只是软约束,这里硬拦"换个措辞"漏网的重复。
- **`AddNode` 写回校验**:唯一写入入口在触库前校验 kind 属于闭合集、payload 非空。为绕过工具直接写图的新写入者预留的最后防线。
- **`deviations` 字段**:`record_fact` 可选记录执行偏离原计划之处,并透出到规划者——让它看见意图**实际怎么跑偏的**,而不只看结果。

### 多脑协同

| 角色 | 职责 | 唤醒方式 |
|---|---|---|
| 规划者 planner | 读态势、判目标、补意图 | 图变即唤醒(另有心跳兜底) |
| 执行者 worker | 领一条意图、执行、写回 | 轮询 frontier(无活时 800ms 退避) |
| **态势研判者 hypothesizer** | 横向综合已确认事实,提出跨资产/跨链路的**假设** | 仅心跳(planner × 3),不抢图变事件 |
| **验证者 verifier** | 独立重放 PoC 的**只读**证据,复核发现 | 仅心跳 |

- **第二 brain**:产出 `hypothesis` 节点(state 机 `open → confirmed | refuted`)。规划者消费 open 假设派验证意图,产出事实后 `resolve_hypothesis` 定案。它剥离了 planner-only 机制(todos/kill/steer/goal 判定),只读 + `record_hypothesis`,从不 wire notify——零跨循环唤醒竞争。
- **验证者**:`report_finding` 此前完全凭 worker 自称,而 worker 最容易自欺(把版本号/CVE 命中当漏洞)。验证者用独立会话重放 PoC 的只读部分,结论写回 finding 节点。
  - **无法复现 ≠ 已证伪**:目标不可达时记 `verified=false` + `issues="目标不可达,待复测"`
  - 结论只写 `payload.verification`,**不改 state**:复核失败是给运营者的证据,不自动 dismiss 一个可能真实的漏洞
  - 未复核(nil)与复核未通过(`verified=false`)在 UI 与 API 上严格区分

### 安全边界

- **提示注入边界**:探索图是共享写面——worker 把目标输出(HTTP 响应、banner、"管理员留言")抄进 fact,下一轮 planner 又把该 fact 重新注入自己的提示,这是从目标直达编排器的现成注入通道。为此新增**代码所有、不可编辑**的提示段,声明图谱内容与目标输出一律是数据不是指令,并给出处置动作。编辑 DB 提示词正文无法把它删掉。
- **验证者只读原则**:需要写入/删除/改口令的操作一律不做。

## 配置开关

全部默认关闭,不配置时行为与上游 ARTEX 一致。

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `LODESTAR_CLI_ENABLED` | 关 | 用 CLI 子进程作 worker 后端(否则全走 norma) |
| `LODESTAR_CLI_COMMAND` | — | CLI 可执行文件(如 `claude`) |
| `LODESTAR_CLI_ARGS` | — | 参数模板,逗号分隔;用字面量 `{{PROMPT}}` 传提示词,否则走 stdin |
| `LODESTAR_CLI_ENV` | — | 传给子进程的额外环境变量 |
| `LODESTAR_BRAIN2_ENABLED` | 关 | 启用态势研判者(第二 brain) |
| `LODESTAR_BRAIN2_MODEL` | 继承 planner | 覆盖第二 brain 的模型 |
| `LODESTAR_VERIFIER_ENABLED` | 关 | 启用发现复核 |
| `LODESTAR_VERIFIER_MODEL` | 继承全局 | 覆盖验证者的模型 |

CLI 后端的**已知上限**(授权实验室场景):子进程内部步骤不落 activity,`search_all_worker_traces` 看不到;子进程自身的 Bash 不受 guard/intercept 拦截。

## 快速开始

启动方式与上游一致(详见 [`README.ARTEX.md`](./README.ARTEX.md)):

```bash
# 开发模式:后端 :8787 + 流量代理 :8788 + 前端 :5173
./dev.sh

# 或编译单二进制
go build -o artex ./cmd/artex && ./artex -addr :8787 -proxy :8788
```

打开 **http://localhost:8787**(首次进入 `/setup` 设置管理员密码)。

## 变更日志

- [`CHANGELOG.LODESTAR.md`](./CHANGELOG.LODESTAR.md) —— **本项目的增量改造**(阶段 1–7)
- [`CHANGELOG.md`](./CHANGELOG.md) —— 上游 ARTEX 的发布记录

## 来源与许可

本项目 vendor 了 ARTEX 源码(`github.com/Autumn-27/artex`,module path 保留),并依赖 [norma](https://github.com/Autumn-27/norma) SDK。ARTEX 与 norma 上游均**未附带 LICENSE 文件**;本仓库为**个人研究、非公开分发**用途。如需再分发或商用,须先联系上游作者获得授权。上游原始 README 保留为 [`README.ARTEX.md`](./README.ARTEX.md)。

Cairn 采用 AGPLv3;本项目仅借鉴其架构思想,未复制其代码。

阶段 7 的角色划分与纪律条文参考了 [sapper-v2](https://github.com/N1nEmAn/sapper-v2)(MIT)的提示词模板。

## 免责声明

**仅供授权测试与研究使用。** 未经明确授权,不得对任何系统、网络、应用或数据进行安全测试、利用或访问。使用者自行承担全部法律与合规责任。
