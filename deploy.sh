#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${NAMESPACE:-sre-system}"
WATCH_NAMESPACE="${WATCH_NAMESPACE:-default}"
IMAGE_REGISTRY="${IMAGE_REGISTRY:-ghcr.io/bojay576}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
USE_LOCAL_IMAGES="${USE_LOCAL_IMAGES:-false}"
SRE_AGENT_IMAGE="${SRE_AGENT_IMAGE:-${IMAGE_REGISTRY}/sre-agent:${IMAGE_TAG}}"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
BOLD='\033[1m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*"; }
step()  { echo -e "\n${CYAN}${BOLD}==> $*${NC}\n"; }
title() { echo -e "${BOLD}$*${NC}"; }

check_prerequisites() {
    step "检查前置条件"
    if ! command -v kubectl &>/dev/null; then
        err "kubectl 未安装，请先安装: https://kubernetes.io/docs/tasks/tools/"
        exit 1
    fi
    if ! kubectl cluster-info &>/dev/null; then
        err "无法连接 Kubernetes 集群，请检查 kubeconfig"
        exit 1
    fi
    info "集群连接正常: $(kubectl config current-context)"
}

render_manifest() {
    local manifest="$1"
    sed -e "s|namespace: sre-system|namespace: ${NAMESPACE}|g" \
        -e "s|value: \"default\"|value: \"${WATCH_NAMESPACE}\"|g" \
        -e "s|image: ghcr.io/bojay576/sre-agent:latest|image: ${SRE_AGENT_IMAGE}|g" \
        "$manifest"
}

apply_manifest() {
    render_manifest "$1" | kubectl apply -f -
}

prepare_image() {
    step "准备容器镜像"
    info "SRE Agent 镜像: ${SRE_AGENT_IMAGE}"
    if [ "${USE_LOCAL_IMAGES}" != "true" ]; then
        info "使用可拉取镜像；如需从当前源码构建本地镜像，请设置 USE_LOCAL_IMAGES=true"
        return
    fi

    if ! command -v docker &>/dev/null; then
        err "USE_LOCAL_IMAGES=true 需要 docker 来构建镜像"
        exit 1
    fi
    docker build -t "${SRE_AGENT_IMAGE}" "${SCRIPT_DIR}/src/sre-agent"

    local context
    context="$(kubectl config current-context 2>/dev/null || true)"
    case "$context" in
        minikube|*-minikube|minikube-*)
            if command -v minikube &>/dev/null; then
                minikube image load "${SRE_AGENT_IMAGE}"
            else
                warn "当前 context 是 ${context}，但未找到 minikube CLI；请手动执行: minikube image load ${SRE_AGENT_IMAGE}"
            fi
            ;;
        kind-*)
            if command -v kind &>/dev/null; then
                kind load docker-image "${SRE_AGENT_IMAGE}" --name "${context#kind-}"
            else
                warn "当前 context 是 ${context}，但未找到 kind CLI；请手动执行: kind load docker-image ${SRE_AGENT_IMAGE} --name ${context#kind-}"
            fi
            ;;
    esac
}

deploy() {
    step "部署 SRE Agent"
    apply_manifest "${SCRIPT_DIR}/apps/namespace.yaml"
    apply_manifest "${SCRIPT_DIR}/apps/sre-agent/rbac.yaml"
    apply_manifest "${SCRIPT_DIR}/apps/sre-agent/deployment.yaml"

    info "等待 SRE Agent 就绪..."
    kubectl wait --for=condition=available deploy/sre-agent -n "${NAMESPACE}" --timeout=180s
}

print_status() {
    step "部署状态"
    kubectl get pods -n "${NAMESPACE}" -l app=sre-agent -o wide
    echo ""
    echo "监控命名空间: ${WATCH_NAMESPACE}"
    echo "常用命令:"
    echo "  kubectl logs -n ${NAMESPACE} deploy/sre-agent"
}

main() {
    title "SRE Agent 部署工具"
    check_prerequisites
    prepare_image
    deploy
    print_status
}

main "$@"
