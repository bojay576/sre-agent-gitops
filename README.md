# sre-agent-gitops

基于 GitOps 模式的 SRE AI Agent Kubernetes 部署仓库。通过声明式 YAML 清单，在 Kubernetes 集群上部署一套 AI 驱动的站点可靠性工程（SRE）代理系统，实现对集群的智能运维管理。

**支持两种 LLM 模式：**
- **本地 Ollama** — 使用集群内 Ollama 服务，数据不出集群
- **外部 API** — 使用 OpenAI 兼容的云端 API（如 OpenAI / Claude / 通义千问等）

## 快速开始（新集群）

```bash
git clone https://github.com/bojay576/sre-agent-gitops.git
cd sre-agent-gitops
chmod +x deploy.sh
./deploy.sh
```

脚本会交互式引导你完成：前置检查 → 存储类配置 → 选择 LLM 模式 → 构建/检查镜像 → 部署 → 等待就绪 → 拉取模型。

## 架构概览

```
┌──────────────────────────────────────────────────────────┐
│                    Kubernetes 集群                         │
│                                                          │
│  namespace: ai-services                                  │
│  ┌──────────────┐    ┌──────────────┐                   │
│  │  AI Gateway   │───▶│  MCP Server  │───▶ MySQL         │
│  │ (NodePort)    │    │  (ClusterIP) │    (ClusterIP)    │
│  └──────┬────────┘    └──────────────┘    HR Database    │
│         │                                                │
│         ▼                                                │
│  ┌──────────────┐                                        │
│  │    Ollama     │  ← 可选：可用外部 API 替代              │
│  │  (NodePort)   │                                        │
│  └──────────────┘                                        │
│                                                          │
│  namespace: default                                      │
│  ┌──────────────┐                                        │
│  │  SRE Agent    │  ← RBAC: get/list/watch/delete        │
│  │               │     pods, events, logs                 │
│  └──────────────┘                                        │
└──────────────────────────────────────────────────────────┘
```

### 组件说明

| 组件 | 说明 | 端口 | 镜像 |
|------|------|------|------|
| **Ollama** | 本地 LLM 推理（可选，可用外部 API 替代） | 11434 | `ollama/ollama:latest` |
| **MySQL** | 关系数据库，存储 HR 示例数据 | 3306 | `mysql:8.0` |
| **MCP Server** | Go 实现的 Model Context Protocol 服务，将数据库操作暴露为 AI 工具 | 8080 | `mcp-hr-server:v1` |
| **AI Gateway** | AI 网关，连接 LLM 与 MCP 工具链 | 30080 | `ai-gateway:v5` |
| **SRE Agent** | Kubernetes 运维代理，具备 Pod 查看/删除等权限 | - | `sre-agent:v1.0` |

### MCP 工具

1. **`read_schema`** — 获取数据库所有表名和字段结构，AI 编写 SQL 前自动调用
2. **`execute_query`** — 执行 SQL 语句（SELECT / INSERT / UPDATE / DELETE 等）

### 数据流

```
用户提问 → AI Gateway → LLM 推理 (Ollama 或外部 API)
                ↓
         LLM 决定调用工具
                ↓
         MCP Server (read_schema / execute_query)
                ↓
            MySQL (hr_db)
                ↓
         查询结果返回 → LLM 生成自然语言回答 → 返回用户
```

## 目录结构

```
sre-agent-gitops/
├── deploy.sh                         # 一键部署脚本
├── apps/
│   ├── ollama/
│   │   └── ollama.yaml               # PVC, Deployment, Service
│   ├── namespace.yaml                 # ai-services 命名空间
│   ├── mysql/
│   │   ├── mysql-secret.yaml          # MySQL 密码 Secret
│   │   ├── mysql-deployment.yaml      # ConfigMap, PVC, Deployment, Service
│   │   └── hr_sample_schema.sql       # 参考 SQL（实际通过 ConfigMap 加载）
│   ├── mcp-agent/
│   │   ├── gateway.yaml               # AI Gateway Service + Deployment
│   │   ├── server-secret.yaml         # MCP Server DSN Secret
│   │   └── server.yaml                # MCP Server Service + Deployment
│   └── sre-agent/
│       ├── deployment.yaml            # SRE Agent Deployment
│       └── rbac.yaml                  # ServiceAccount, ClusterRole, ClusterRoleBinding
├── src/
│   ├── ai-gateway/
│   │   ├── main.go                    # AI Gateway 源码
│   │   └── Dockerfile                 # Gateway 镜像构建
│   ├── sre-agent/
│   │   ├── main.go                    # SRE Agent 源码
│   │   └── Dockerfile                 # Agent 镜像构建
│   └── mcp-hr-server/
│       ├── main.go                    # MCP Server Go 源码
│       ├── Dockerfile                 # 多阶段构建
│       └── go.mod / go.sum            # Go 模块定义
├── .gitignore
└── README.md
```

## 新电脑部署指南

在一台全新的电脑上部署本项目，需要满足以下条件。整个过程约需 **10-20 分钟**（取决于网络和模型下载速度）。

### 硬件要求

| 模式 | CPU | 内存 | 磁盘 | GPU（可选） |
|------|-----|------|------|------------|
| 本地 Ollama | 4 核+ | 16 GB+ | 50 GB+ | 推荐 NVIDIA GPU |
| 仅外部 API | 2 核 | 4 GB | 20 GB | 不需要 |

> **说明：** 如果只用外部 API 模式（不需要本地 Ollama），硬件要求大幅降低，普通笔记本即可。
> 本地 Ollama 模式需要运行 `qwen3:4b`（约 2.5 GB），建议 16 GB 以上内存。

### 软件依赖

| 工具 | 版本要求 | 安装方式 |
|------|---------|---------|
| **kubectl** | v1.24+ | `brew install kubectl` / [官方文档](https://kubernetes.io/docs/tasks/tools/) |
| **容器运行时** | — | Docker Desktop / containerd / nerdctl |
| **Go**（仅构建 MCP Server） | 1.21+ | `brew install go` / [golang.org](https://go.dev/dl/) |

可选：
- **nerdctl** — 用于导入 `.tar` 镜像到 containerd（k3s 环境推荐）
- **Docker** — 用于构建 MCP Server 镜像
- **Helm** — 可选，用于安装 OpenEBS

### Kubernetes 集群

你需要一个运行的 Kubernetes 集群。根据场景选择：

| 方案 | 适用场景 | 安装命令 |
|------|---------|---------|
| **k3s** | Linux 单机，资源占用低 | `curl -sfL https://get.k3s.io \| sh` |
| **Docker Desktop** | macOS/Windows，开箱即用 | 设置中启用 Kubernetes |
| **minikube** | 跨平台，功能完整 | `minikube start --cpus 4 --memory 8192` |
| **Kind** | CI/测试环境 | `kind create cluster` |
| 云集群（TKE/ACK/EKS 等） | 生产环境 | 各云厂商控制台 |

集群就绪后验证：
```bash
kubectl cluster-info
kubectl get nodes
```

### 存储

项目使用 PVC 持久化数据，需要集群有可用的 **StorageClass**。

`deploy.sh` 脚本会自动检测，如果没有存储类，会引导你安装 **OpenEBS**（轻量级本地存储）：

```bash
# 手动安装 OpenEBS（可选）
kubectl apply -f https://openebs.github.io/charts/openebs-operator.yaml
```

如果你的集群已有其他存储类（如 `local-path`、`gp2`、`managed-csi` 等），部署时选择使用已有存储类即可。
`deploy.sh` 会在应用清单时把 PVC 中的 `openebs-hostpath` 替换为你选择的 StorageClass；如果手动部署，请先编辑 `apps/ollama/ollama.yaml` 和 `apps/mysql/mysql-deployment.yaml` 中的 `storageClassName`。

### 镜像获取

这是新电脑部署最关键的环节。五个组件镜像的来源：

| 组件 | 镜像 | 来源 | 新电脑上如何获取 |
|------|------|------|-----------------|
| **Ollama** | `ollama/ollama:latest` | Docker Hub | 自动拉取（需网络） |
| **MySQL** | `mysql:8.0` | Docker Hub | 自动拉取（需网络） |
| **MCP Server** | `mcp-hr-server:v1` | **本仓库源码构建** | `cd src/mcp-hr-server && docker build -t mcp-hr-server:v1 .` |
| **AI Gateway** | `ai-gateway:v5` | **本仓库源码构建** | `cd src/ai-gateway && docker build -t ai-gateway:v5 .` |
| **SRE Agent** | `sre-agent:v1.0` | **本仓库源码构建** | `cd src/sre-agent && docker build -t sre-agent:v1.0 .` |

快速检查镜像是否就绪：
```bash
# Docker 环境
docker images | grep -E "mcp-hr-server|ai-gateway|sre-agent"

# containerd 环境（k3s）
nerdctl -n k8s.io image ls | grep -E "mcp-hr-server|ai-gateway|sre-agent"
```

如果使用 Kind 或 minikube，`deploy.sh` 在 Docker 构建完成后会自动把本地镜像加载到当前集群节点：

- Kind context（如 `kind-kind`）：执行 `kind load docker-image`
- minikube context：执行 `minikube image load`

如果缺少对应 CLI，脚本会输出需要手动执行的加载命令。

### 网络

- 集群需要能访问 **Docker Hub**（拉取 `ollama/ollama`、`mysql:8.0`）
- 如果用外部 API 模式，需要能访问对应的 API 端点（如 `api.openai.com`）
- AI Gateway 通过 **NodePort 30080** 暴露，确保节点 IP 可访问
- Ollama 调试端口固定为 **NodePort 31134**（本地 Ollama 模式）

### 部署流程图

```
新电脑
  │
  ├─ 1. 安装 kubectl + Docker/k3s
  ├─ 2. 启动 Kubernetes 集群
  ├─ 3. 安装 OpenEBS（或使用已有存储类）
  ├─ 4. 构建/导入本地镜像
  │     ├── mcp-hr-server:v1   ← 本仓库有源码，docker build
  │     ├── ai-gateway:v5      ← 本仓库有源码，docker build
  │     └── sre-agent:v1.0     ← 本仓库有源码，docker build
  ├─ 5. 运行 ./deploy.sh
  │     ├── 选择 Ollama 或外部 API 模式
  │     ├── 自动部署所有组件
  │     └── 等待 Pod 就绪
  └─ 6. 验证：curl http://<node-ip>:30080
```

### 快速验证

部署完成后，运行以下命令确认一切正常：

```bash
# 所有 Pod 应处于 Running 状态
kubectl get pods -n ai-services
kubectl get pods -n default -l app=sre-agent

# MCP Server 应打印数据库连接成功
kubectl logs -n ai-services deploy/mcp-hr-server | head -5
# 预期输出: "Successfully connected to MySQL"
#           "Starting MCP Server on :8080/sse"

# AI Gateway 应能访问
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
curl -s "http://${NODE_IP}:30080" && echo "Gateway OK"
```

---

## LLM 模式

### 模式一：本地 Ollama（默认）

使用集群内 Ollama 推理，数据不出集群，无需外部 API Key。

```bash
./deploy.sh  # 选择 [1] 本地 Ollama
```

默认模型 `qwen3:4b`，可修改 `apps/mcp-agent/gateway.yaml` 中的 `LLM_MODEL` 环境变量切换模型。部署后脚本会自动拉取模型。

### 模式二：外部 API

使用 OpenAI 兼容的云端 API 服务，无需部署 Ollama，只需 API Key。Gateway 通过 `LLM_PROVIDER=openai` 发送 `/v1/chat/completions` 格式请求；本地模式则通过 `LLM_PROVIDER=ollama` 发送 Ollama `/api/chat` 格式请求。

```bash
./deploy.sh  # 选择 [2] 外部 API
# 输入 API URL、模型名称、API Key
```

**兼容的 API 服务示例：**

| 服务 | API URL | 模型名示例 |
|------|---------|-----------|
| OpenAI | `https://api.openai.com/v1/chat/completions` | `gpt-4o` |
| Anthropic (via 代理) | 需要一个 OpenAI 兼容代理 | `claude-sonnet-4-6` |
| 阿里通义千问 | `https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions` | `qwen-plus` |
| DeepSeek | `https://api.deepseek.com/v1/chat/completions` | `deepseek-chat` |
| 其他兼容服务 | 填写对应的 API 地址 | 对应的模型名称 |

## 手动部署步骤

如果不使用 `deploy.sh` 脚本，可以手动逐步部署：

### 1. 创建命名空间

```bash
kubectl apply -f apps/namespace.yaml
```

### 2. 部署 Ollama（可选，用外部 API 则跳过）

```bash
kubectl apply -f apps/ollama/ollama.yaml
# 等待就绪后拉取模型
kubectl exec -n ai-services deploy/ollama -- ollama pull qwen3:4b
```

### 3. 部署 MySQL

```bash
kubectl apply -f apps/mysql/mysql-secret.yaml
kubectl apply -f apps/mysql/mysql-deployment.yaml
```

初始化脚本会自动创建 `hr_db` 数据库、`departments` 和 `employees` 表，并插入示例数据。

### 4. 部署 MCP Server

```bash
kubectl apply -f apps/mcp-agent/server-secret.yaml
kubectl apply -f apps/mcp-agent/server.yaml
```

### 5. 配置并部署 AI Gateway

编辑 `apps/mcp-agent/gateway.yaml` 中的环境变量：

```yaml
env:
- name: LLM_PROVIDER
  value: "ollama"                                # ollama | openai | custom
- name: LLM_API_URL
  value: "http://ollama-service:11434/api/chat"  # 或 OpenAI 兼容 API 地址
- name: LLM_MODEL
  value: "qwen3:4b"                              # 或外部模型名
```

然后部署：
```bash
kubectl apply -f apps/mcp-agent/gateway.yaml
```

### 6. 部署 SRE Agent

```bash
kubectl apply -f apps/sre-agent/rbac.yaml
kubectl apply -f apps/sre-agent/deployment.yaml
```

## 镜像说明

| 镜像 | 来源 | 说明 |
|------|------|------|
| `ollama/ollama:latest` | Docker Hub | 官方镜像，默认即可 |
| `mysql:8.0` | Docker Hub | 官方镜像 |
| `mcp-hr-server:v1` | 本地构建 | 见下方「构建本地镜像」 |
| `ai-gateway:v5` | 本地构建 | 见下方「构建本地镜像」 |
| `sre-agent:v1.0` | 本地构建 | 见下方「构建本地镜像」 |

## CI/CD

仓库包含 GitHub Actions workflow：`.github/workflows/build.yml`。

- Pull Request：对 `src/ai-gateway`、`src/mcp-hr-server`、`src/sre-agent` 执行 `go vet`、`go test`，并构建 Docker 镜像但不推送
- Push 到 `main`：构建并推送镜像到 GHCR，标签包含 `latest` 和 `sha-<commit>`

默认发布路径：

```text
ghcr.io/bojay576/ai-gateway:latest
ghcr.io/bojay576/mcp-hr-server:latest
ghcr.io/bojay576/sre-agent:latest
```

### 使用私有镜像仓库（可选）

如果需要在离线环境或私有仓库使用，可参考以下方式：

```bash
# 示例：从华为云 SWR 拉取（需先在 ollama.yaml 中取消 imagePullSecrets 注释并替换 image）
kubectl create secret docker-registry swr-secret \
  --docker-server=swr.cn-north-4.myhuaweicloud.com \
  --docker-username=<your-username> \
  --docker-password=<your-password> \
  -n ai-services
```

然后编辑 `apps/ollama/ollama.yaml`，取消 `imagePullSecrets` 注释并替换 image 字段。

## 验证部署

```bash
# 检查所有 Pods
kubectl get pods -n ai-services
kubectl get pods -n default -l app=sre-agent

# 验证 MCP Server 连接 MySQL
kubectl logs -n ai-services deploy/mcp-hr-server
# 应看到: "Successfully connected to MySQL" 和 "Starting MCP Server on :8080/sse"

# 访问 AI Gateway（获取任意节点 IP）
kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'
curl http://<node-ip>:30080
```

## 配置参考

### AI Gateway 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `LLM_PROVIDER` | LLM API 类型：`ollama`、`openai` 或 `custom` | `ollama` |
| `LLM_API_URL` | LLM API 地址 | `http://ollama-service:11434/api/chat` |
| `LLM_MODEL` | 使用的模型名称 | `qwen3:4b` |
| `LLM_API_KEY` | API Key（外部 API 模式，通过 Secret 注入） | - |
| `MCP_SERVER_URL` | MCP Server SSE 地址 | `http://mcp-server-service:8080/sse` |

`OLLAMA_URL` 和 `OLLAMA_MODEL` 仍作为兼容变量保留，新配置建议使用 `LLM_API_URL` 和 `LLM_MODEL`。

### MCP Server 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DSN` | MySQL 连接串 | `root:native@tcp(mysql-service:3306)/hr_db` |

### SRE Agent RBAC 权限

| 资源 | 操作 |
|------|------|
| `pods`, `pods/log`, `events` | get, list, watch |
| `pods` | delete |

## 构建本地镜像

```bash
cd src/mcp-hr-server
docker build -t mcp-hr-server:v1 .

cd ../ai-gateway
docker build -t ai-gateway:v5 .

cd ../sre-agent
docker build -t sre-agent:v1.0 .
```

## 常见问题

**Q: Pod 一直 Pending？**
检查 PVC 状态：`kubectl get pvc -n ai-services`。如果 PVC 无法绑定，先安装 OpenEBS：`kubectl apply -f https://openebs.github.io/charts/openebs-operator.yaml`

**Q: 外部 API 模式请求失败？**
确认 `LLM_PROVIDER=openai`、`LLM_API_URL` 指向 `/v1/chat/completions` 兼容端点，并且 `gateway-llm-secret` 中的 `llm-api-key` 有效。使用 Ollama 时保持 `LLM_PROVIDER=ollama` 和 `/api/chat` 地址。

**Q: 如何切换模型？**
修改 `apps/mcp-agent/gateway.yaml` 中的 `LLM_MODEL` 值，然后 `kubectl apply` 重新应用。

## 许可证

MIT
