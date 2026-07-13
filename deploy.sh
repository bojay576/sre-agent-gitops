#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  SRE Agent 一键部署脚本
#  支持三种模式:
#    - basic : 传统模式，仅巡检不接入 AI
#    - ollama: 本地 Ollama LLM（无需 API Key）
#    - api   : 外部 API 服务（需提供 API Key 和 URL）
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${NAMESPACE:-sre-system}"
WATCH_NAMESPACE="${WATCH_NAMESPACE:-default}"
IMAGE_REGISTRY="${IMAGE_REGISTRY:-ghcr.io/bojay576}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
USE_LOCAL_IMAGES="${USE_LOCAL_IMAGES:-false}"
SRE_AGENT_IMAGE="${SRE_AGENT_IMAGE:-${IMAGE_REGISTRY}/sre-agent:${IMAGE_TAG}}"

# LLM 配置（由 choose_mode 填充）
DEPLOY_MODE="basic"
LLM_PROVIDER=""
LLM_API_URL=""
LLM_MODEL=""
LLM_API_KEY=""
AUTO_HEAL=""
AUTO_HEAL_DRY_RUN="true"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
BOLD='\033[1m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERR]${NC}   $*"; }
step()  { echo -e "\n${CYAN}${BOLD}==> $*${NC}\n"; }
title() { echo -e "${BOLD}$*${NC}"; }

# ---- 1. 前置检查 ----
check_prerequisites() {
    step "检查前置条件"

    if ! command -v kubectl &>/dev/null; then
        err "kubectl 未安装，请先安装: https://kubernetes.io/docs/tasks/tools/"
        exit 1
    fi
    info "kubectl: $(kubectl version --client --short 2>/dev/null || kubectl version --client)"

    if ! kubectl cluster-info &>/dev/null; then
        err "无法连接 Kubernetes 集群，请检查 kubeconfig"
        exit 1
    fi
    info "集群连接正常: $(kubectl config current-context)"
}

# ---- 2. 选择部署模式 ----
choose_mode() {
    step "选择部署模式"

    echo "  请选择 LLM 模式:"
    echo "    [1] 传统模式（默认）— 仅巡检不接入 AI，无需额外配置"
    echo "    [2] 本地 Ollama — 使用本地大模型，无需 API Key"
    echo "        模型如 qwen2.5:7b，数据不出集群"
    echo "    [3] 云端 API — 使用 OpenAI 兼容的 API 服务"
    echo "        需提供 API URL、Key 和模型名称"
    echo ""
    read -r -p "  请选择 [1-3] (默认 1): " mode_choice
    mode_choice="${mode_choice:-1}"

    case "$mode_choice" in
        1)
            DEPLOY_MODE="basic"
            info "模式: 传统模式（仅巡检）"
            ;;
        2)
            DEPLOY_MODE="ollama"
            LLM_PROVIDER="ollama"

            read -r -p "  Ollama API 地址: " api_url
            LLM_API_URL="${api_url:-http://ollama:11434}"
            read -r -p "  模型名称 (如 qwen2.5:7b, llama3.2:3b): " api_model
            LLM_MODEL="${api_model:-qwen2.5:7b}"

            info "模式: Ollama (${LLM_MODEL})"
            configure_auto_heal
            ;;
        3)
            DEPLOY_MODE="api"
            LLM_PROVIDER="openai"

            read -r -p "  API URL (OpenAI 兼容格式): " api_url
            LLM_API_URL="${api_url:-https://api.openai.com/v1}"
            read -r -p "  模型名称 (如 gpt-4o-mini, claude-sonnet-4-6): " api_model
            LLM_MODEL="${api_model:-gpt-4o-mini}"
            read -r -sp "  API Key: " api_key
            echo ""
            if [ -z "$api_key" ]; then
                warn "未提供 API Key，将以无认证模式运行"
            fi
            LLM_API_KEY="${api_key}"

            info "模式: 云端 API (${LLM_MODEL})"
            configure_auto_heal
            ;;
        *)
            err "无效选择"
            exit 1
            ;;
    esac
}

# ---- 自愈配置 ----
configure_auto_heal() {
    echo ""
    read -r -p "  是否启用自动自愈？（根据 LLM 建议自动删除重建故障 Pod）[y/N]: " heal_yn
    if [[ "$heal_yn" == "y" ]] || [[ "$heal_yn" == "Y" ]]; then
        AUTO_HEAL="true"
        read -r -p "  关闭 dry-run 模式？（默认只打印不执行）[y/N]: " dry_yn
        if [[ "$dry_yn" == "y" ]] || [[ "$dry_yn" == "Y" ]]; then
            AUTO_HEAL_DRY_RUN="false"
        else
            AUTO_HEAL_DRY_RUN="true"
        fi
        info "自愈已启用（dry-run=${AUTO_HEAL_DRY_RUN}）"
    fi
}

# ---- 3. 准备镜像 ----
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

# ---- 4. 渲染清单 ----
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

# ---- 5. 部署 ----
do_deploy() {
    step "开始部署"

    # 创建命名空间
    info "创建命名空间: ${NAMESPACE}"
    apply_manifest "${SCRIPT_DIR}/apps/namespace.yaml"

    # RBAC
    info "创建 RBAC..."
    apply_manifest "${SCRIPT_DIR}/apps/sre-agent/rbac.yaml"

    # 部署 Deployment（注入 LLM 配置）
    info "创建 Deployment..."
    local dep_file="${SCRIPT_DIR}/apps/sre-agent/deployment.yaml"
    local tmp
    tmp="$(mktemp)"
    render_manifest "$dep_file" > "$tmp"

    if [ "$DEPLOY_MODE" != "basic" ]; then
        # 在 resources 前插入 LLM 环境变量
        sed -i '' '/resources:/i\
        - name: LLM_PROVIDER\
          value: "'"${LLM_PROVIDER}"'"\
        - name: LLM_API_URL\
          value: "'"${LLM_API_URL}"'"\
        - name: LLM_MODEL\
          value: "'"${LLM_MODEL}"'"\
' "$tmp"

        if [ -n "${LLM_API_KEY:-}" ]; then
            sed -i '' '/resources:/i\
        - name: LLM_API_KEY\
          value: "'"${LLM_API_KEY}"'"\
' "$tmp"
        fi

        if [ -n "${AUTO_HEAL:-}" ]; then
            sed -i '' '/resources:/i\
        - name: AUTO_HEAL\
          value: "'"${AUTO_HEAL}"'"\
        - name: AUTO_HEAL_DRY_RUN\
          value: "'"${AUTO_HEAL_DRY_RUN:-true}"'"\
' "$tmp"
        fi
    fi

    kubectl apply -f "$tmp"
    rm -f "$tmp"

    info "等待 SRE Agent 就绪..."
    kubectl wait --for=condition=available deploy/sre-agent -n "${NAMESPACE}" --timeout=180s
}

# ---- 6. 输出状态 ----
print_status() {
    step "部署状态"

    echo ""
    title "═══════════════════════════════════════════════════"
    title "  SRE Agent — 部署完成"
    title "═══════════════════════════════════════════════════"
    echo ""

    kubectl get pods -n "${NAMESPACE}" -l app=sre-agent -o wide
    echo ""

    echo "📋 配置信息:"
    echo "   监控命名空间: ${WATCH_NAMESPACE}"
    echo "   部署命名空间: ${NAMESPACE}"
    if [ "$DEPLOY_MODE" != "basic" ]; then
        echo "   LLM Provider: ${LLM_PROVIDER}"
        echo "   LLM Model:    ${LLM_MODEL}"
        if [ -n "${AUTO_HEAL:-}" ]; then
            echo "   Auto Heal:    ${AUTO_HEAL} (dry-run: ${AUTO_HEAL_DRY_RUN})"
        fi
    fi
    echo ""

    echo "📋 常用命令:"
    echo "   kubectl logs -n ${NAMESPACE} deploy/sre-agent -f   # 查看实时日志"
    echo "   ./uninstall.sh                                      # 卸载"
    echo ""

    if [ "$DEPLOY_MODE" != "basic" ]; then
        echo "💡 部署故障 Pod 测试 LLM 分析:"
        echo "   kubectl apply -f tests/fault-pods/"
        echo ""
    fi
}

# ---- 主流程 ----
main() {
    title "╔══════════════════════════════════════════════╗"
    title "║   SRE Agent — 一键部署工具                   ║"
    title "╚══════════════════════════════════════════════╝"
    echo ""

    check_prerequisites
    choose_mode
    prepare_image
    do_deploy
    print_status
}

main "$@"
