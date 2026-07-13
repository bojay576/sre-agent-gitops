# 故障 Pod 测试集

用于测试 SRE Agent 的 Pod 巡检能力。这些 Pod 会模拟各种故障状态，部署后 Agent 应该能检测到并输出相应日志。

## 快速部署

一键部署所有故障 Pod：

```bash
kubectl apply -f tests/fault-pods/
```

## 故障场景

| 文件 | 模拟状态 | Agent 预期输出 |
|------|---------|---------------|
| `01-pending.yaml` | **Pending** — 请求资源超限，调度不成功 | `pod attention phase=Pending` |
| `02-crashloop.yaml` | **CrashLoopBackOff** — 容器启动即退出 | `container not ready restarts=N` |
| `03-imagepull.yaml` | **ImagePullBackOff** — 镜像不存在 | `pod attention phase=Pending` |
| `04-oomkill.yaml` | **OOMKilled** — 内存超限被杀 | `container not ready restarts=N` |
| `05-init-fail.yaml` | **Init 容器失败** — Init 容器出错，主容器不启动 | `pod attention phase=Init:Error` |

## 验证 Agent 检测

部署后观察 Agent 日志：

```bash
kubectl logs -n sre-system deploy/sre-agent -f
```

预期输出示例：

```
pod attention namespace=default name=fault-pending phase=Pending
pod attention namespace=default name=fault-imagepull phase=Pending
pod attention namespace=default name=fault-init-fail phase=PodInitializing
container not ready pod=fault-crashloop container=app restarts=5
container not ready pod=fault-oomkill container=app restarts=3
cluster check namespace=default pods=8 not_ready=5 restarts=8
```

## 清理

```bash
kubectl delete -f tests/fault-pods/
```
