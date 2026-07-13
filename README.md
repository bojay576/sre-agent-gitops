# SRE Agent

智能运维 Agent，定时扫描 Kubernetes 指定命名空间的 Pod 状态，检测异常并通过可选的 LLM 分析给出处理建议，支持自动自愈。

默认部署命名空间 `sre-system`，默认监控命名空间 `default`。

---

## 工作原理

```
┌─────────────────────────────────────────────┐
│              Kubernetes 集群                  │
│                                              │
│  ┌──────────────┐   GET /api/v1/.../pods    │
│  │  SRE Agent   │ ────────────────────────► │
│  │  (Pod)       │◄──────────────────────── │
│  │              │   JSON (PodList)          │
│  └──┬───┬───────┘                           │
│     │   │                                    │
│     │   └─ 故障 Pod? ──► LLM 分析 ──► 自愈   │
│     │                     │                  │
│     └─ 定时轮询: POLL_INTERVAL_SECONDS       │
└─────────────────────────────────────────────┘
```

**认证方式**：通过挂载的 Service Account Token（`/var/run/secrets/kubernetes.io/serviceaccount/token`）和 CA 证书向 K8s API Server 发起 HTTPS 请求。

**单次请求**：`GET /api/v1/namespaces/{namespace}/pods`，返回所有 Pod 及其状态。

**输出**：全部通过标准日志（stdout），格式为 `key=value` 便于 grep 或对接日志系统。

**LLM 集成**（可选）：设置 `LLM_PROVIDER` 后，检测到故障 Pod 时将状态发给大模型，返回分析结果和处理建议。支持 Ollama（本地）和 OpenAI-compatible API（OpenAI / Anthropic / 兼容代理）。

**自愈**（可选）：设置 `AUTO_HEAL=true` 后，自动执行 LLM 建议的操作（如 `delete_pod`）。默认 dry-run 模式只打印不执行。

---

## 快速开始

```bash
# 交互式部署（会提示选择 LLM 模式）
./deploy.sh

# 指定监控命名空间
WATCH_NAMESPACE=production ./deploy.sh
```

部署过程中会提示选择 LLM 模式：
- **传统模式** — 仅巡检，不接入 AI
- **本地 Ollama** — 使用本地大模型（如 qwen2.5:7b），无需 API Key
- **云端 API** — 使用 OpenAI 兼容 API，需提供 URL 和 Key

随后可配置**自动自愈**：根据 LLM 分析结果自动删除重建故障 Pod。

> 由于脚本包含交互式输入，如果通过 `! ./deploy.sh` 运行，提示会在当前会话中显示。

查看日志：

```bash
kubectl logs -n sre-system deploy/sre-agent -f
```

卸载：

```bash
./uninstall.sh
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WATCH_NAMESPACE` | `default` | 监控目标命名空间 |
| `POLL_INTERVAL_SECONDS` | `30` | 轮询间隔（最小 5s） |
| `NAMESPACE` | `sre-system` | Agent 自身部署命名空间 |
| `SRE_AGENT_IMAGE` | `ghcr.io/bojay576/sre-agent:latest` | 容器镜像 |
| `USE_LOCAL_IMAGES` | `false` | 从本地源码构建并加载镜像 |
| `KUBERNETES_API_URL` | 自动检测 | 手动指定 K8s API Server 地址 |
| `LLM_PROVIDER` | _(空)_ | 大模型提供商：`openai` 或 `ollama` |
| `LLM_API_URL` | _(空)_ | API 地址，如 `http://ollama:11434` |
| `LLM_API_KEY` | _(空)_ | API Key（OpenAI 等云服务需要） |
| `LLM_MODEL` | `gpt-4o-mini` | 模型名称 |
| `AUTO_HEAL` | `false` | 启用自动自愈（设为 `true`） |
| `AUTO_HEAL_DRY_RUN` | `true` | dry-run 模式，只打印不执行 |

---

## LLM 集成

支持两种模式：

### Ollama（本地部署，无需外网）

```bash
LLM_PROVIDER=ollama \
LLM_API_URL=http://ollama:11434 \
LLM_MODEL=qwen2.5:7b \
./deploy.sh
```

### OpenAI-compatible API（OpenAI / Anthropic / 兼容代理）

```bash
LLM_PROVIDER=openai \
LLM_API_URL=https://api.openai.com/v1 \
LLM_API_KEY=sk-xxxx \
LLM_MODEL=gpt-4o-mini \
./deploy.sh
```

### 自愈控制

```bash
# dry-run 模式（默认）：仅打印将要执行的操作
AUTO_HEAL=true ./deploy.sh

# 关闭 dry-run，真正执行操作
AUTO_HEAL=true AUTO_HEAL_DRY_RUN=false ./deploy.sh
```

LLM 分析后返回的结构化结果包含：
- `severity`: `low | medium | high | critical`
- `can_auto_heal`: 是否适合自动处理
- `actions`: 建议操作列表（如 `delete_pod`）

自愈执行器根据 `can_auto_heal` 和 `AUTO_HEAL` 开关决定是否执行，同一 Pod 在 5 分钟内不重复操作。

---

## 日志说明

```
SRE Agent started namespace=default poll_interval=30s api_server=https://10.96.0.1:443
pod attention namespace=default name=example phase=Pending
container not ready pod=example container=app restarts=3
cluster check namespace=default pods=3 not_ready=1 restarts=3
LLM enabled provider=ollama model=qwen2.5:7b
remedy: LLM analysis severity=high can_auto_heal=true actions=1 auto_heal=true dry_run=false
remedy: analysis: Pod fault-crashloop is in CrashLoopBackOff due to container exiting immediately on start.
remedy: delete_pod default/fault-crashloop reason="Restart loop, delete to allow fresh start"
remedy: ✅ deleted pod default/fault-crashloop (HTTP 200)
```

---

## 故障测试

提供 5 种故障 Pod 用于验证 Agent 检测和 LLM 自愈：

```bash
# 部署故障 Pod
kubectl apply -f tests/fault-pods/

# 观察 Agent 检测和自愈
kubectl logs -n sre-system deploy/sre-agent -f

# 清理
kubectl delete -f tests/fault-pods/
```

详见 [tests/fault-pods/](tests/fault-pods/)。

---

## 开发

```bash
# 从本地源码构建并部署
USE_LOCAL_IMAGES=true ./deploy.sh

# 推送到镜像仓库后部署
docker build -t ghcr.io/your-org/sre-agent:custom src/sre-agent
docker push ghcr.io/your-org/sre-agent:custom
SRE_AGENT_IMAGE=ghcr.io/your-org/sre-agent:custom ./deploy.sh
```

### 文件结构

```
.
├── apps/
│   ├── namespace.yaml             # sre-system 命名空间
│   └── sre-agent/
│       ├── deployment.yaml        # Deployment（含 LLM 配置注释）
│       └── rbac.yaml              # ServiceAccount + ClusterRole + Binding
├── src/sre-agent/
│   ├── main.go                    # 主循环 + Pod 检查
│   ├── llm.go                     # LLM 客户端（Ollama / OpenAI）
│   ├── remedy.go                  # 自愈执行器
│   ├── Dockerfile
│   └── go.mod
├── tests/fault-pods/              # 故障 Pod 测试集
├── deploy.sh
└── uninstall.sh
```

## 回退

```bash
# 指定旧版本镜像
SRE_AGENT_IMAGE=ghcr.io/bojay576/sre-agent:v1.0.0 ./deploy.sh

# 原地回滚
kubectl rollout undo deploy/sre-agent -n sre-system

# 查看部署历史
kubectl rollout history deploy/sre-agent -n sre-system
```
