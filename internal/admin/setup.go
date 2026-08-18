package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rent-scout/internal/config"
	"rent-scout/internal/store"
)

const (
	setupTotalSteps = 5
	setupDoneStep   = 6
)

// handleSetup 首次引导：步骤1鉴权必填，后续可跳过
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleSetupPost(w, r)
		return
	}
	step, _ := strconv.Atoi(r.URL.Query().Get("step"))
	if step < 0 {
		step = 0
	}
	if step > setupDoneStep {
		step = setupDoneStep
	}
	kv := CurrentConfigKV(s.db)
	env := config.KVToSecrets(kv)
	cookieRawHint := "粘贴 cookie 原文；留空不修改"
	if raw := env.Collector.Douban.CookieRaw; raw != "" {
		cookieRawHint = fmt.Sprintf("已保存 · 长度 %d；留空不修改", len(raw))
	}
	data := mergePageCtx(s.pageCtx(r, ""), map[string]any{
		"Step":          step,
		"Total":         setupTotalSteps,
		"App":           config.KVToApp(kv),
		"Env":           env,
		"KV":            kv,
		"CookieRawHint": cookieRawHint,
		"SkipNav":       true,
	})
	if err := s.tmpl.ExecuteTemplate(w, "setup", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	step, _ := strconv.Atoi(r.PostFormValue("step"))
	action := r.PostFormValue("action")
	kv := CurrentConfigKV(s.db)

	// step 6：导入后的完成页——可选填访问令牌开启鉴权，随后完成引导
	if step == setupDoneStep {
		if tok := strings.TrimSpace(r.PostFormValue("admin.token")); tok != "" {
			if err := store.SetConfigBatch(s.db, map[string]string{
				"admin.auth_required": "true",
				"admin.token":         tok,
			}); err != nil {
				http.Error(w, "保存失败: "+err.Error(), http.StatusInternalServerError)
				return
			}
			_ = s.rt.ReloadOnce()
		}
		s.finishSetup(w, r, kv)
		return
	}

	if action == "skip" {
		if step <= 1 {
			http.Error(w, "鉴权步骤不可跳过", http.StatusBadRequest)
			return
		}
		// 末步跳过 = 完成设置
		if step >= setupTotalSteps {
			s.finishSetup(w, r, kv)
			return
		}
		s.redirectSetup(w, r, step+1)
		return
	}

	var section string
	switch step {
	case 1:
		section = "admin"
	case 2:
		section = "general"
	case 3:
		section = "collector"
	case 4:
		section = "filter"
	case 5:
		section = "notifier"
	default:
		http.Error(w, "无效步骤", http.StatusBadRequest)
		return
	}

	updates := ParseSectionForm(r.PostForm, section, kv)
	if step == 1 {
		if updates["admin.auth_required"] == "true" && updates["admin.token"] == "" {
			if old := kv["admin.token"]; old == "" {
				http.Error(w, "启用鉴权时必须设置 Token", http.StatusBadRequest)
				return
			}
		}
	}
	MergeDefaultsInto(updates, kv)
	merged := config.MergeKV(kv, updates)
	if step == 3 {
		if errs := config.ValidateSecrets(config.KVToSecrets(merged)); len(errs) > 0 {
			http.Error(w, "校验失败: "+strings.Join(errs, "; "), http.StatusBadRequest)
			return
		}
	}
	if len(updates) > 0 {
		if err := store.SetConfigBatch(s.db, updates); err != nil {
			http.Error(w, "保存失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = s.rt.ReloadOnce()
	}

	if action == "finish" || step >= setupTotalSteps {
		s.finishSetup(w, r, merged)
		return
	}

	s.redirectSetup(w, r, step+1)
}

// finishSetup 校验全量配置、种子默认规则、标记 setup 完成
func (s *Server) finishSetup(w http.ResponseWriter, r *http.Request, kv map[string]string) {
	if errs := config.ValidateApp(config.KVToApp(kv)); len(errs) > 0 {
		http.Error(w, "校验失败: "+strings.Join(errs, "; "), http.StatusBadRequest)
		return
	}
	if errs := config.ValidateSecrets(config.KVToSecrets(kv)); len(errs) > 0 {
		http.Error(w, "校验失败: "+strings.Join(errs, "; "), http.StatusBadRequest)
		return
	}
	// 无启用规则时种子默认地点白名单（规格 §4）
	if err := s.db.EnsureDefaultRule(); err != nil {
		http.Error(w, "种子默认规则失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := store.SetConfigBatch(s.db, map[string]string{config.KeySetupCompleted: "true"}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.rt.ReloadOnce()
	q := url.Values{}
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	} else if t := s.rt.Get().Admin.Token; t != "" {
		q.Set("token", t)
	}
	http.Redirect(w, r, "/admin?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) redirectSetup(w http.ResponseWriter, r *http.Request, step int) {
	if step > setupTotalSteps {
		step = setupTotalSteps
	}
	q := url.Values{"step": {strconv.Itoa(step)}}
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	} else if t := s.rt.Get().Admin.Token; t != "" {
		q.Set("token", t)
	}
	http.Redirect(w, r, "/admin/setup?"+q.Encode(), http.StatusSeeOther)
}

// setupGate setup 未完成时拦截管理页，强制进引导
func (s *Server) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store.IsSetupComplete(s.db) {
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if path == "/admin/setup" || path == "/admin/setup/import-defaults" || path == "/admin/config/save" || path == CookieTestPath || path == CookieCloudTestPath || path == "/healthz" || path == "/metrics" || path == "/f" || path == "/h" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/admin") || path == "/" {
			http.Redirect(w, r, "/admin/setup?"+r.URL.Query().Encode(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleImportDefaults 一键导入内置默认配置（POST /admin/setup/import-defaults）
func (s *Server) handleImportDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
		return
	}
	if err := store.SetConfigBatch(s.db, config.DefaultKV()); err != nil {
		http.Error(w, "导入默认配置失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.rt.ReloadOnce()
	q := url.Values{}
	q.Set("step", strconv.Itoa(setupDoneStep))
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	}
	target := "/admin/setup"
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// setupStepTitle 引导步骤标题（模板 func）
func setupStepTitle(step int) string {
	switch step {
	case 0:
		return "选择初始化方式"
	case 1:
		return "访问鉴权（必填）"
	case 6:
		return "完成设置"
	case 2:
		return "常规设置"
	case 3:
		return "采集与 CookieCloud"
	case 4:
		return "筛选与 AI"
	case 5:
		return "通知（可选）"
	default:
		return fmt.Sprintf("步骤 %d", step)
	}
}
