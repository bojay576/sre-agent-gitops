# SRE Agent

智能运维 Agent 项目，用于定时扫描 Kubernetes 中指定命名空间的 Pod 状态，发现异常 Pod、非 Ready 容器和重启次数，并写入日志。

默认部署命名空间是 `sre-system`，默认监控命名空间是 `default`。

## 部署

```bash
./deploy.sh
```

指定监控命名空间：

```bash
WATCH_NAMESPACE=mcp-services ./deploy.sh
```

常用环境变量：

```bash
NAMESPACE=sre-system
WATCH_NAMESPACE=default
POLL_INTERVAL_SECONDS=30
SRE_AGENT_IMAGE=ghcr.io/bojay576/sre-agent:latest
USE_LOCAL_IMAGES=false
```

## 查看发现结果

当前版本通过日志输出发现结果：

```bash
kubectl logs -n sre-system deploy/sre-agent -f
```

日志示例：

```text
pod attention namespace=default name=example phase=Pending
container not ready pod=example container=app restarts=3
cluster check namespace=default pods=3 not_ready=1 restarts=3
```

## 目录

```text
apps/
  namespace.yaml
  sre-agent/
src/
  sre-agent/
deploy.sh
```

## 权限

SRE Agent 使用 ClusterRole 读取 Pod、Pod 日志和 Events，并保留删除 Pod 的权限，为后续自动修复能力预留。
