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

脚本会交互式引导你完成：前置检查 → 存储类配置 → 选择 LLM 模式 → 导入镜像 → 部署 → 等待就绪 → 拉取模型。

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
│   │   └── ollama.yaml               # Namespace, PVC, Deployment, Service
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
│   └── mcp-hr-server/
│       ├── main.go                    # MCP Server Go 源码
│       ├── Dockerfile                 # 多阶段构建
│       ├── go.mod / go.sum            # Go 模块定义
│       └── mcp-hr-server.tar          # 预构建镜像（可离线导入）
├── .gitignore
└── README.md
```

## 前置条件

- **Kubernetes 集群** v1.24+（k3s / minikube / Kind / 云原生集群均可）
- **kubectl** 已配置并可操作目标集群
- 集群能够拉取 Docker Hub 镜像（`ollama/ollama`、`mysql:8.0`）

> **注意：** `ai-gateway:v5`、`sre-agent:v1.0` 是本地镜像，需要自行构建并导入集群。`mcp-hr-server:v1` 可从提供的 tar 包导入。

## LLM 模式

### 模式一：本地 Ollama（默认）

使用集群内 Ollama 推理，数据不出集群，无需外部 API Key。

```bash
./deploy.sh  # 选择 [1] 本地 Ollama
```

默认模型 `qwen3:4b`，可修改 `apps/mcp-agent/gateway.yaml` 中的 `OLLAMA_MODEL` 环境变量切换模型。部署后脚本会自动拉取模型。

### 模式二：外部 API

使用 OpenAI 兼容的云端 API 服务，无需部署 Ollama，只需 API Key。

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

> **注意：** AI Gateway 使用 Ollama 格式（`/api/chat`）调用 LLM。如果外部 API 是 OpenAI 格式（`/v1/chat/completions`），需要在中间加一层代理转换（如 [litellm](https://github.com/BerriAI/litellm)），或者确认 gateway 镜像是否支持 OpenAI 格式。

## 手动部署步骤

如果不使用 `deploy.sh` 脚本，可以手动逐步部署：

### 1. 创建命名空间

```bash
kubectl create namespace ai-services
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
- name: OLLAMA_URL
  value: "http://ollama-service:11434/api/chat"  # 或外部 API 地址
- name: OLLAMA_MODEL
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
| `mcp-hr-server:v1` | 本地构建 | 见下方「构建 MCP Server」，提供 tar 包可离线导入 |
| `ai-gateway:v5` | 自行构建 | 网关应用，需自行构建镜像 |
| `sre-agent:v1.0` | 自行构建 | SRE Agent，需自行构建镜像 |

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
| `OLLAMA_URL` | LLM API 地址 | `http://ollama-service:11434/api/chat` |
| `OLLAMA_MODEL` | 使用的模型名称 | `qwen3:4b` |
| `LLM_API_KEY` | API Key（外部 API 模式，通过 Secret 注入） | - |

### MCP Server 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DSN` | MySQL 连接串 | `root:native@tcp(mysql-service:3306)/hr_db` |

### SRE Agent RBAC 权限

| 资源 | 操作 |
|------|------|
| `pods`, `pods/log`, `events` | get, list, watch |
| `pods` | delete |

## 构建 MCP Server

```bash
cd src/mcp-hr-server

# 本地编译
CGO_ENABLED=0 GOOS=linux go build -o mcp-server .

# Docker 构建
docker build -t mcp-hr-server:v1 .

# 导出 tar（离线导入用）
docker save mcp-hr-server:v1 -o mcp-hr-server.tar

# 导入到 containerd（k3s 环境）
nerdctl -n k8s.io load -i mcp-hr-server.tar
```

## 常见问题

**Q: Pod 一直 Pending？**
检查 PVC 状态：`kubectl get pvc -n ai-services`。如果 PVC 无法绑定，先安装 OpenEBS：`kubectl apply -f https://openebs.github.io/charts/openebs-operator.yaml`

**Q: 外部 API 模式请求失败？**
确认 gateway 镜像是否支持 OpenAI 格式的 API。当前 gateway 使用 Ollama 格式（`/api/chat`），如果外部 API 是 OpenAI 格式（`/v1/chat/completions`），可使用 [litellm](https://github.com/BerriAI/litellm) 做格式转换代理。

**Q: 如何切换模型？**
修改 `apps/mcp-agent/gateway.yaml` 中的 `OLLAMA_MODEL` 值，然后 `kubectl apply` 重新应用。

## 许可证

MIT
