package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 自愈执行器
// ---------------------------------------------------------------------------

type remedier struct {
	client   *http.Client
	cfg      config
	autoHeal bool // AUTO_HEAL=true 才真正执行动作
	dryRun   bool // 默认 true，仅打印不执行（安全模式）
}

// podActionHistory 记录已执行的操作，避免重复
var podActionHistory = make(map[string]time.Time)

func newRemedier(client *http.Client, cfg config) *remedier {
	autoHeal := os.Getenv("AUTO_HEAL") == "true"
	dryRun := os.Getenv("AUTO_HEAL_DRY_RUN") != "false" // 默认 dry-run

	return &remedier{
		client:   client,
		cfg:      cfg,
		autoHeal: autoHeal,
		dryRun:   dryRun,
	}
}

// executeActions 按顺序执行 LLM 建议的操作
func (r *remedier) executeActions(ctx context.Context, analysis *LLMAnalysis) {
	if analysis == nil || len(analysis.Actions) == 0 {
		return
	}

	log.Printf("remedy: LLM analysis severity=%s can_auto_heal=%v actions=%d auto_heal=%v dry_run=%v",
		analysis.Severity, analysis.CanAutoHeal, len(analysis.Actions), r.autoHeal, r.dryRun)
	log.Printf("remedy: analysis: %s", analysis.Analysis)

	if !analysis.CanAutoHeal {
		log.Printf("remedy: LLM determined auto-heal not appropriate, skipping actions")
		return
	}

	if !r.autoHeal {
		log.Printf("remedy: AUTO_HEAL not enabled, would-execute: %s", formatActions(analysis.Actions))
		return
	}

	for _, action := range analysis.Actions {
		select {
		case <-ctx.Done():
			log.Printf("remedy: context cancelled, stopping actions")
			return
		default:
		}

		// 去重：同一 Pod 在 5 分钟内不重复执行
		key := action.Namespace + "/" + action.Target + "/" + action.Type
		if last, ok := podActionHistory[key]; ok {
			if time.Since(last) < 5*time.Minute {
				log.Printf("remedy: skip %s (executed %v ago)", key, time.Since(last).Round(time.Second))
				continue
			}
		}

		r.executeOne(ctx, action)
		podActionHistory[key] = time.Now()
	}
}

func (r *remedier) executeOne(ctx context.Context, action LLMAction) {
	switch action.Type {
	case "delete_pod":
		r.deletePod(ctx, action)
	default:
		log.Printf("remedy: unknown action type=%q target=%s", action.Type, action.Target)
	}
}

// deletePod 通过 K8s API 删除 Pod
func (r *remedier) deletePod(ctx context.Context, action LLMAction) {
	ns := action.Namespace
	if ns == "" {
		ns = r.cfg.Namespace
	}

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s", r.cfg.APIServer, ns, action.Target)

	log.Printf("remedy: delete_pod %s/%s reason=%q", ns, action.Target, action.Reason)

	if r.dryRun {
		log.Printf("remedy: [DRY RUN] would DELETE %s", url)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		log.Printf("remedy: delete request failed: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		log.Printf("remedy: delete %s/%s failed: %v", ns, action.Target, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("remedy: ✅ deleted pod %s/%s (HTTP %d)", ns, action.Target, resp.StatusCode)
	} else {
		log.Printf("remedy: ❌ delete pod %s/%s returned HTTP %d", ns, action.Target, resp.StatusCode)
	}
}

func formatActions(actions []LLMAction) string {
	parts := make([]string, len(actions))
	for i, a := range actions {
		parts[i] = fmt.Sprintf("%s %s/%s", a.Type, a.Namespace, a.Target)
	}
	return strings.Join(parts, ", ")
}
