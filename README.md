# Lodestar

混合多脑的自主渗透测试智能体 —— 在双图上朝目标导航。

个人研究项目。基于 [ARTEX](https://github.com/Autumn-27/ARTEX) 二次开发,融合 [Cairn](https://github.com/oritera/Cairn) 的核心思想。

## 这是什么

Lodestar 把两个开源渗透 agent 的优点合到一起:

- **ARTEX 作基座**:双图架构(资产图 + 探索图)、事件驱动引擎、norma 进程内 harness、产品层(UI/流量录制/审批门)。
- **Cairn 作思想**:可换脑(把通用 agent CLI 当 worker)、两阶段收尾兜底、受控状态空间搜索。

核心新增能力是**混合多脑**:同一个引擎既能用 ARTEX 的进程内 norma worker(功能完整、支持跨 worker 过程共享),也能把 `claude` / `codex` 等通用 agent CLI 当作 worker 委派(白嫖各厂商完整 agentic 能力、异构互补)。引擎按 intent 特性分派后端。

## 架构要点

- **AgentBackend 接口**:抽自 ARTEX 现有 worker Execute 签名。`NormaWorker`(进程内)与 `CLIWorker`(子进程委派 + 批处理写回)对引擎同一接口。
- **混合分派 `pickBackend`**:需过程共享的深链走 norma,异构视角的独立探索走 CLI,失败降级回 norma。
- **CLIWorker 批处理契约**:壳注入定向图上下文 → CLI 跑完整循环 → 解析结构化结果经现有 ToolSet 写回双图。写图单一路径,校验/血缘/notify 全自动统一。
- **Intent Admission**:canonical fingerprint 去重,收口到 store 层。

详见开发文档(待补)。

## 来源与许可

本项目 vendor 了 ARTEX 源码(`github.com/Autumn-27/artex`,module path 保留),并依赖 [norma](https://github.com/Autumn-27/norma) SDK。ARTEX 与 norma 上游均**未附带 LICENSE 文件**;本仓库为**个人研究、非公开分发**用途。如需再分发或商用,须先联系上游作者获得授权。上游原始 README 保留为 [`README.ARTEX.md`](./README.ARTEX.md)。

Cairn 采用 AGPLv3;本项目仅借鉴其架构思想,未复制其代码。

## 免责声明

**仅供授权测试与研究使用。** 未经明确授权,不得对任何系统、网络、应用或数据进行安全测试、利用或访问。使用者自行承担全部法律与合规责任。
