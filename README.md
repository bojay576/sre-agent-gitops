# SRE Agent

智能运维 Agent 项目，用于定时扫描 Kubernetes 中指定命名空间的 Pod 状态，发现异常 Pod、非 Ready 容器和重启次数，并写入日志。

默认部署命名空间是 `sre-system`，默认监控命名空间是 `default`。

---

## Agent 是如何工作的？

### 架构图

```
  ┌─────────────────────────────────────────────┐
  │              Kubernetes 集群                  │
  │                                              │
  │  ┌──────────────┐   HTTP (TLS)   ┌────────┐ │
  │  │  SRE Agent   │ ──────────────►│ K8s    │ │
  │  │  (Pod)       │◄──────────────│ API    │ │
  │  │              │   JSON 响应    │ Server │ │
  │  └──────┬───────┘               └────────┘ │
  │         │                                   │
  │         │ 定时轮询 POLL_INTERVAL_SECONDS      │
  │         ▼                                   │
  │  ┌──────────────┐                           │
  │  │  日志输出     │                           │
  │  │  - pod attention                          │
  │  │  - container not ready                    │
  │  │  - cluster check                          │
  │  └──────────────┘                           │
  └─────────────────────────────────────────────┘
```

### 调用了什么 API？

SRE Agent **不调用任何大模型 API**。它是一个传统的运维守护进程，直接调用 **Kubernetes API Server**：

| 请求 | 说明 |
|------|------|
| `GET /api/v1/namespaces/{namespace}/pods` | 列出指定命名空间的所有 Pod 及其状态 |

Agent 使用 **Service Account** 进行认证：

- **Token 文件**: `/var/run/secrets/kubernetes.io/serviceaccount/token`（由 K8s 自动挂载）
- **CA 证书**: `/var/run/secrets/kubernetes.io/serviceaccount/ca.crt`（验证 API Server 的 TLS 证书）
- **权限**: 通过 ClusterRole `sre-agent-role` 授予 `get/list/watch pods`、`get/list/watch events`、`delete pods` 权限

### Agent 做了什么？

1. 每 `POLL_INTERVAL_SECONDS`（默认 30s）轮询一次 K8s API
2. 找出**非 Running/Succeeded** 状态的 Pod → 输出 `pod attention`
3. 找出 **Ready=false** 的容器 → 输出 `container not ready`
4. 汇总统计 → 输出 `cluster check`

全部结果通过 **标准日志输出**，不存储到数据库也不发送告警。

---

## 使用方式

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WATCH_NAMESPACE` | `default` | 要监控的命名空间 |
| `POLL_INTERVAL_SECONDS` | `30` | 轮询间隔（秒），最小 5 |
| `NAMESPACE` | `sre-system` | Agent 自身的部署命名空间 |
| `SRE_AGENT_IMAGE` | `ghcr.io/bojay576/sre-agent:latest` | 容器镜像 |
| `USE_LOCAL_IMAGES` | `false` | 是否从本地源码构建镜像 |
| `KUBERNETES_API_URL` | 自动从 `KUBERNETES_SERVICE_HOST` 获取 | 手动指定 API Server 地址 |

### 部署

```bash
# 默认部署（监控 default 命名空间）
./deploy.sh

# 指定监控命名空间
WATCH_NAMESPACE=production ./deploy.sh

# 部署到自定义命名空间
NAMESPACE=my-observability ./deploy.sh

# 使用自定义镜像
SRE_AGENT_IMAGE=my-registry/sre-agent:v1.2.0 ./deploy.sh

# 从当前源码构建本地镜像并部署
USE_LOCAL_IMAGES=true ./deploy.sh
```

### 查看结果

```bash
kubectl logs -n sre-system deploy/sre-agent -f
```

日志示例：

```text
SRE Agent started namespace=default poll_interval=30s api_server=https://10.96.0.1:443
pod attention namespace=default name=example phase=Pending
container not ready pod=example container=app restarts=3
cluster check namespace=default pods=3 not_ready=1 restarts=3
```

---

## 如何更改和扩展？

### 修改监控配置

直接修改环境变量重新部署即可，无需改代码：

```bash
POLL_INTERVAL_SECONDS=10 WATCH_NAMESPACE=staging ./deploy.sh
```

### 修改源码

1. 编辑 `src/sre-agent/main.go`
2. 构建镜像并部署：

```bash
USE_LOCAL_IMAGES=true SRE_AGENT_IMAGE=sre-agent:dev ./deploy.sh
```

或者推送到镜像仓库：

```bash
docker build -t ghcr.io/your-org/sre-agent:custom src/sre-agent
docker push ghcr.io/your-org/sre-agent:custom
SRE_AGENT_IMAGE=ghcr.io/your-org/sre-agent:custom ./deploy.sh
```

### 修改 RBAC 权限

编辑 `apps/sre-agent/rbac.yaml`，修改后：

```bash
kubectl apply -f apps/sre-agent/rbac.yaml
```

### 新增功能示例

在 `main.go` 中添加新的检查逻辑（如检查 Events、检查 PVC 状态等），然后按上述步骤重新构建部署即可。

---

## 卸载

```bash
# 删除 Deployment 和 RBAC（保留命名空间）
kubectl delete deploy sre-agent -n sre-system
kubectl delete ClusterRoleBinding sre-agent-binding
kubectl delete ClusterRole sre-agent-role
kubectl delete sa sre-agent-sa -n sre-system

# 完全卸载（删除所有资源，包括命名空间）
kubectl delete ns sre-system

# 或使用 manifest 文件反向删除
kubectl delete -f apps/sre-agent/deployment.yaml
kubectl delete -f apps/sre-agent/rbac.yaml
kubectl delete -f apps/namespace.yaml
```

## 回退到旧版本

```bash
# 方法一：指定旧版本镜像重新部署
SRE_AGENT_IMAGE=ghcr.io/bojay576/sre-agent:v1.0.0 ./deploy.sh

# 方法二：直接在 Deployment 上修改镜像
kubectl set image deploy/sre-agent -n sre-system agent=ghcr.io/bojay576/sre-agent:v1.0.0

# 方法三：回滚到上一个版本
kubectl rollout undo deploy/sre-agent -n sre-system

# 查看部署历史
kubectl rollout history deploy/sre-agent -n sre-system
```

---

## 目录结构

```text
.
├── apps/                          # K8s 部署清单
│   ├── namespace.yaml             # sre-system 命名空间
│   └── sre-agent/                 # Agent 相关资源
│       ├── deployment.yaml        # Deployment
│       └── rbac.yaml              # ServiceAccount + ClusterRole + Binding
├── src/
│   └── sre-agent/                 # Go 源码
│       ├── main.go                # 主程序
│       ├── Dockerfile             # 构建镜像
│       └── go.mod                 # Go 模块定义
└── deploy.sh                      # 一键部署脚本
```

---

## 项目文件说明

| 文件 | 作用 |
|------|------|
| `apps/namespace.yaml` | 创建 `sre-system` 命名空间 |
| `apps/sre-agent/rbac.yaml` | 定义 ServiceAccount、ClusterRole（pod 只读+删除权限）、ClusterRoleBinding |
| `apps/sre-agent/deployment.yaml` | Deployment 配置，含环境变量和资源限制 |
| `src/sre-agent/main.go` | Agent 核心逻辑：Service Account 认证、调用 K8s API、Pod 状态检查 |
| `src/sre-agent/Dockerfile` | 基于 scratch 构建的极小镜像 |
| `deploy.sh` | 部署脚本，支持环境变量覆盖 |

---

## 许可

MIT
