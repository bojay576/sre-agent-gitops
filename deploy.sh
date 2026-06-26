#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  sre-agent-gitops  一键部署脚本
#  支持两种模式:
#    - ollama : 本地 Ollama LLM（默认，无需 API Key）
#    - api    : 外部 API 服务（需提供 API Key 和 URL）
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="ai-services"
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

    # kubectl
    if ! command -v kubectl &>/dev/null; then
        err "kubectl 未安装，请先安装: https://kubernetes.io/docs/tasks/tools/"
        exit 1
    fi
    info "kubectl: $(kubectl version --client --short 2>/dev/null || kubectl version --client)"

    # 集群连接
    if ! kubectl cluster-info &>/dev/null; then
        err "无法连接 Kubernetes 集群，请检查 kubeconfig"
        exit 1
    fi
    info "集群连接正常: $(kubectl config current-context)"

    # 容器运行时 (用于导入镜像)
    RUNTIME=""
    if command -v nerdctl &>/dev/null; then
        RUNTIME="nerdctl"
    elif command -v docker &>/dev/null; then
        RUNTIME="docker"
    elif command -v ctr &>/dev/null; then
        RUNTIME="ctr"
    elif command -v crictl &>/dev/null; then
        RUNTIME="crictl"
    fi
    if [ -z "$RUNTIME" ]; then
        warn "未检测到容器运行时 (nerdctl/docker/ctr)，将跳过镜像导入步骤"
    else
        info "容器运行时: ${RUNTIME}"
    fi
}

# ---- 2. 检查/创建 StorageClass ----
ensure_storage() {
    step "检查存储类"

    if kubectl get storageclass openebs-hostpath &>/dev/null; then
        info "StorageClass 'openebs-hostpath' 已存在"
        return
    fi

    warn "未找到 'openebs-hostpath' StorageClass"
    echo "  PVC 需要存储类才能创建。选项:"
    echo "    [1] 安装 OpenEBS (适用于大多数集群)"
    echo "    [2] 使用集群已有的其他 StorageClass"
    echo "    [3] 跳过 (将导致 PVC 处于 Pending 状态)"
    read -r -p "  请选择 [1-3] (默认 1): " sc_choice
    sc_choice="${sc_choice:-1}"

    case "$sc_choice" in
        1)
            install_openebs
            ;;
        2)
            read -r -p "  输入已有 StorageClass 名称: " alt_sc
            if kubectl get storageclass "$alt_sc" &>/dev/null; then
                info "使用已有 StorageClass: $alt_sc"
                # 替换所有 YAML 中的 storageClassName
                export STORAGE_CLASS="$alt_sc"
            else
                err "StorageClass '$alt_sc' 不存在"
                exit 1
            fi
            ;;
        3)
            warn "跳过存储类检查，PVC 可能无法绑定"
            ;;
    esac
}

install_openebs() {
    info "安装 OpenEBS..."
    kubectl apply -f https://openebs.github.io/charts/openebs-operator.yaml
    info "等待 OpenEBS 就绪..."
    kubectl wait --for=condition=available -n openebs deployment/localpv-provisioner --timeout=120s 2>/dev/null || true
    # 设置默认 StorageClass
    kubectl patch storageclass openebs-hostpath -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}' 2>/dev/null || true
    info "OpenEBS 安装完成"
}

# ---- 3. 选择部署模式 ----
choose_mode() {
    step "选择 LLM 模式"

    echo "  请选择 AI Gateway 使用的 LLM 后端:"
    echo "    [1] 本地 Ollama (默认) — 使用集群内 Ollama 服务，无需外部 API"
    echo "        模型: qwen3:4b，端侧推理，数据不出集群"
    echo "    [2] 外部 API — 使用 OpenAI 兼容的云端 API 服务"
    echo "        需要提供 API URL 和 API Key"
    echo ""
    read -r -p "  请选择 [1-2] (默认 1): " mode_choice
    mode_choice="${mode_choice:-1}"

    case "$mode_choice" in
        1)
            DEPLOY_MODE="ollama"
            OLLAMA_URL="http://ollama-service:11434/api/chat"
            OLLAMA_MODEL="qwen3:4b"
            NEED_OLLAMA=true
            info "模式: 本地 Ollama (${OLLAMA_MODEL})"
            ;;
        2)
            DEPLOY_MODE="api"
            NEED_OLLAMA=false

            read -r -p "  API URL (兼容 OpenAI/Ollama 格式): " api_url
            OLLAMA_URL="${api_url}"
            read -r -p "  模型名称 (如 gpt-4o, claude-sonnet-4-6, qwen-plus): " api_model
            OLLAMA_MODEL="${api_model}"
            read -r -sp "  API Key: " api_key
            echo ""
            if [ -z "$api_key" ]; then
                warn "未提供 API Key，将以无认证模式运行"
            fi
            LLM_API_KEY="${api_key}"
            info "模式: 外部 API (${OLLAMA_MODEL} @ ${OLLAMA_URL})"
            ;;
        *)
            err "无效选择"
            exit 1
            ;;
    esac
}

# ---- 4. 导入镜像 ----
import_images() {
    step "导入容器镜像"

    local loaded=0

    # MCP Server 镜像
    local tar_file="${SCRIPT_DIR}/src/mcp-hr-server/mcp-hr-server.tar"
    if [ -f "$tar_file" ]; then
        if load_image_from_tar "$tar_file" "mcp-hr-server:v1"; then
            loaded=$((loaded + 1))
        fi
    else
        warn "MCP Server tar 不存在: ${tar_file}，请先构建: cd src/mcp-hr-server && docker build -t mcp-hr-server:v1 ."
    fi

    # SRE Agent (本地镜像)
    if docker image inspect sre-agent:v1.0 &>/dev/null 2>&1; then
        info "sre-agent:v1.0 镜像已存在"
    else
        warn "sre-agent:v1.0 镜像不存在，将尝试拉取"
    fi

    # AI Gateway (本地镜像)
    if docker image inspect ai-gateway:v5 &>/dev/null 2>&1; then
        info "ai-gateway:v5 镜像已存在"
    else
        warn "ai-gateway:v5 镜像不存在，将尝试拉取"
    fi

    return 0
}

load_image_from_tar() {
    local tar_file="$1"
    local image_name="$2"

    # 先检查镜像是否已存在
    case "$RUNTIME" in
        nerdctl)
            if nerdctl -n k8s.io image inspect "$image_name" &>/dev/null 2>&1; then
                info "镜像已存在: ${image_name}"
                return 0
            fi
            info "导入镜像到 containerd: ${image_name}"
            nerdctl -n k8s.io load -i "$tar_file"
            ;;
        docker)
            if docker image inspect "$image_name" &>/dev/null 2>&1; then
                info "镜像已存在: ${image_name}"
                return 0
            fi
            info "导入镜像到 Docker: ${image_name}"
            docker load -i "$tar_file"
            ;;
        ctr)
            if ctr -n k8s.io image ls | grep -q "${image_name}"; then
                info "镜像已存在: ${image_name}"
                return 0
            fi
            info "导入镜像到 containerd (ctr): ${image_name}"
            ctr -n k8s.io image import "$tar_file"
            ;;
        *)
            warn "无法导入镜像 ${image_name}（无可用运行时）"
            return 1
            ;;
    esac
    info "镜像导入成功: ${image_name}"
    return 0
}

# ---- 5. 部署 ----
do_deploy() {
    step "开始部署"

    # 5a. 创建 namespace（确保在其他资源之前）
    info "创建命名空间: ${NAMESPACE}"
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

    # 5b. 创建 Secrets 先
    info "创建 Secrets..."
    kubectl apply -f "${SCRIPT_DIR}/apps/mysql/mysql-secret.yaml"
    kubectl apply -f "${SCRIPT_DIR}/apps/mcp-agent/server-secret.yaml"

    # Gateway Secret (API Key)
    if [ "${DEPLOY_MODE}" = "api" ] && [ -n "${LLM_API_KEY:-}" ]; then
        info "创建 LLM API Key Secret..."
        kubectl create secret generic gateway-llm-secret \
            --from-literal=llm-api-key="${LLM_API_KEY}" \
            -n "${NAMESPACE}" \
            --dry-run=client -o yaml | kubectl apply -f -
    else
        # 本地模式也创建一个空 Secret，避免 Deployment 引用失败
        kubectl create secret generic gateway-llm-secret \
            --from-literal=llm-api-key="" \
            -n "${NAMESPACE}" \
            --dry-run=client -o yaml | kubectl apply -f -
    fi

    # 5c. 部署 Ollama（仅在 ollama 模式下）
    if [ "${NEED_OLLAMA}" = "true" ]; then
        info "部署 Ollama（使用 Docker Hub 官方镜像）..."
        kubectl apply -f "${SCRIPT_DIR}/apps/ollama/ollama.yaml"

        # 提示：如果使用了私有仓库镜像，需自行创建 pull secret
        # 编辑 apps/ollama/ollama.yaml 取消 imagePullSecrets 注释并替换镜像地址
    fi

    # 5d. 部署 MySQL
    info "部署 MySQL..."
    kubectl apply -f "${SCRIPT_DIR}/apps/mysql/mysql-deployment.yaml"

    # 5e. 部署 MCP Server
    info "部署 MCP Server..."
    kubectl apply -f "${SCRIPT_DIR}/apps/mcp-agent/server.yaml"

    # 5f. 部署 AI Gateway
    info "部署 AI Gateway..."

    # 用实际值替换 gateway.yaml 中的占位变量
    local gateway_yaml="${SCRIPT_DIR}/apps/mcp-agent/gateway.yaml"
    if [ -n "${OLLAMA_URL:-}" ] && [ -n "${OLLAMA_MODEL:-}" ]; then
        # 生成带配置的临时 manifest
        sed -e "s|value: \"http://ollama-service:11434/api/chat\"|value: \"${OLLAMA_URL}\"|g" \
            -e "s|value: \"qwen3:4b\"|value: \"${OLLAMA_MODEL}\"|g" \
            "$gateway_yaml" | kubectl apply -f -
    else
        kubectl apply -f "$gateway_yaml"
    fi

    # 5g. 部署 SRE Agent
    info "部署 SRE Agent..."
    kubectl apply -f "${SCRIPT_DIR}/apps/sre-agent/rbac.yaml"
    kubectl apply -f "${SCRIPT_DIR}/apps/sre-agent/deployment.yaml"

    info "所有清单已提交"
}

# ---- 6. 等待就绪 ----
wait_ready() {
    step "等待 Pod 就绪"

    local deployments=()
    [ "${NEED_OLLAMA}" = "true" ] && deployments+=("deploy/ollama")
    deployments+=("deploy/mysql" "deploy/mcp-hr-server" "deploy/ai-gateway" "deploy/sre-agent")

    for dep in "${deployments[@]}"; do
        local dep_ns="${NAMESPACE}"
        [ "$dep" = "deploy/sre-agent" ] && dep_ns="default"

        info "等待 ${dep} (ns: ${dep_ns})..."
        if kubectl wait --for=condition=available "${dep}" -n "${dep_ns}" --timeout=180s 2>/dev/null; then
            info "${dep} 就绪"
        else
            warn "${dep} 在 180s 内未就绪，请手动检查"
        fi
    done
}

# ---- 7. 拉取模型 ----
pull_model() {
    if [ "${NEED_OLLAMA}" != "true" ]; then
        return
    fi

    step "拉取 Ollama 模型"

    if ! kubectl get pod -n "${NAMESPACE}" -l app=ollama -o name &>/dev/null; then
        warn "Ollama Pod 未运行，跳过模型拉取"
        return
    fi

    local ollama_pod
    ollama_pod=$(kubectl get pod -n "${NAMESPACE}" -l app=ollama -o jsonpath='{.items[0].metadata.name}')

    info "检查已安装模型..."
    local installed
    installed=$(kubectl exec -n "${NAMESPACE}" "${ollama_pod}" -- ollama list 2>/dev/null || echo "")

    if echo "$installed" | grep -q "${OLLAMA_MODEL}"; then
        info "模型 ${OLLAMA_MODEL} 已存在"
    else
        info "拉取模型: ${OLLAMA_MODEL}（可能需要几分钟，取决于模型大小）..."
        kubectl exec -n "${NAMESPACE}" "${ollama_pod}" -- ollama pull "${OLLAMA_MODEL}"
        info "模型拉取完成"
    fi
}

# ---- 8. 输出状态 ----
print_status() {
    step "部署状态"

    echo ""
    title "═══════════════════════════════════════════════════"
    title "  SRE Agent GitOps — 部署完成"
    title "═══════════════════════════════════════════════════"
    echo ""

    echo "📦 ai-services 命名空间:"
    kubectl get pods -n "${NAMESPACE}" -o wide 2>/dev/null || true
    echo ""
    echo "📦 default 命名空间 (SRE Agent):"
    kubectl get pods -n default -l app=sre-agent -o wide 2>/dev/null || true
    echo ""

    echo "🌐 服务端口:"
    echo "   AI Gateway:  NodePort 30080"
    if [ "${NEED_OLLAMA}" = "true" ]; then
        echo "   Ollama:      NodePort (动态分配)"
    fi
    echo "   MCP Server:  ClusterIP :8080"
    echo "   MySQL:       ClusterIP :3306"
    echo ""

    # 尝试获取节点 IP
    local node_ip
    node_ip=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || echo "<node-ip>")
    echo "🔗 访问 AI Gateway:"
    echo "   curl http://${node_ip}:30080"
    echo ""

    echo "📋 常用命令:"
    echo "   kubectl logs -n ${NAMESPACE} deploy/ai-gateway    # AI Gateway 日志"
    echo "   kubectl logs -n ${NAMESPACE} deploy/mcp-hr-server # MCP Server 日志"
    echo "   kubectl logs -n ${NAMESPACE} deploy/ollama        # Ollama 日志"
    echo "   kubectl logs -n default deploy/sre-agent          # SRE Agent 日志"
    echo ""
}

# ---- 主流程 ----
main() {
    echo ""
    title "╔══════════════════════════════════════════════╗"
    title "║   SRE Agent GitOps — 一键部署工具            ║"
    title "╚══════════════════════════════════════════════╝"
    echo ""

    check_prerequisites
    ensure_storage
    choose_mode
    import_images
    do_deploy
    wait_ready
    pull_model
    print_status
}

main "$@"
