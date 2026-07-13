#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${NAMESPACE:-sre-system}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
BOLD='\033[1m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*"; }
step()  { echo -e "\n${CYAN}${BOLD}==> $*${NC}"; }

step "卸载 SRE Agent（命名空间: ${NAMESPACE}）"

# 1. 删除 Deployment
info "删除 Deployment..."
kubectl delete deploy sre-agent -n "${NAMESPACE}" --ignore-not-found --wait

# 2. 删除 RBAC 资源
info "删除 RBAC..."
kubectl delete ClusterRoleBinding sre-agent-binding --ignore-not-found
kubectl delete ClusterRole sre-agent-role --ignore-not-found
kubectl delete sa sre-agent-sa -n "${NAMESPACE}" --ignore-not-found

# 3. 删除命名空间（包含其下所有资源）
info "删除命名空间 ${NAMESPACE}..."
kubectl delete ns "${NAMESPACE}" --ignore-not-found --wait

# 4. 清理故障测试 Pod（如存在）
info "清理故障测试 Pod..."
kubectl delete -f tests/fault-pods/ --ignore-not-found 2>/dev/null || true

step "卸载完成"
info "SRE Agent 及相关资源已全部删除"
echo -e "\n确认清理结果:"
kubectl get pods -n "${NAMESPACE}" 2>/dev/null || echo "  ✓ 命名空间 ${NAMESPACE} 不存在或已清空"
