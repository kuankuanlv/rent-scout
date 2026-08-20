package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rent-scout/internal/admin/onboard"
	"rent-scout/internal/admin/ports"
	"rent-scout/internal/admin/rules"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// handleConfig 配置管理页与各分区独立保存（二级 Tab：仅渲染当前 tab）
func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/admin/config/save" {
		h.handleConfigSectionSave(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tab := normalizeConfigTab(r.URL.Query().Get("tab"))
	token := r.URL.Query().Get("token")
	data := ports.MergePageCtx(ports.PageCtx(h.opts.RT, r, "config"), map[string]any{
		"Tab":     tab,
		"Tabs":    configTabs,
		"Onboard": onboard.OnboardHint{},
	})
	if ok := r.URL.Query().Get("ok"); ok != "" {
		if ok == "import" {
			data["Message"] = "配置已导入"
		} else {
			data["Message"] = "分区「" + ok + "」已保存"
		}
	}
	if r.URL.Query().Get("restart") == "1" {
		data["NeedRestart"] = true
	}
	if msg := r.URL.Query().Get("err"); msg != "" {
		data["Error"] = msg
	}

	if tab == "rules" {
		rows, err := rules.LoadRuleRows(h.opts.DB)
		if err != nil {
			http.Error(w, "查询失败", http.StatusInternalServerError)
			return
		}
		data["Rules"] = rows
		data["BuiltInAIValue"] = models.BuiltInAIRuleValue
	} else {
		kv := CurrentConfigKV(h.opts.DB)
		app := h.opts.RT.Get()
		env := h.opts.RT.Secrets()
		secID := tabToSectionID(tab)
		sec := sectionByID(buildConfigSections(app, env, kv), secID)
		if sec == nil {
			http.Error(w, "未知配置分区", http.StatusBadRequest)
			return
		}
		data["Section"] = sec
		data["Onboard"] = onboard.OnboardForTab(tab, app, env, token)
		if tab == "general" {
			history, _ := store.ListConfigHistory(h.opts.DB, 20)
			data["History"] = history
		}
	}

	if err := h.opts.Tmpl.ExecuteTemplate(w, "config", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleConfigSectionSave(w http.ResponseWriter, r *http.Request) {
	if err := parseAdminForm(r); err != nil {
		if ports.WantsJSON(r) {
			ports.WriteJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "解析表单失败"})
			return
		}
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	section := r.FormValue("section")
	if _, ok := config.SectionKeys[section]; !ok {
		if ports.WantsJSON(r) {
			ports.WriteJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "未知配置分区"})
			return
		}
		http.Error(w, "未知配置分区", http.StatusBadRequest)
		return
	}
	tab := sectionIDToTab(section)
	rawKV, _ := store.GetConfigMap(h.opts.DB)
	updates := ParseSectionForm(r.Form, section, rawKV)
	needRestart := len(ChangedRestartKeys(rawKV, updates)) > 0
	if _, err := h.saveUpdates(updates); err != nil {
		if ports.WantsJSON(r) {
			ports.WriteJSONStatus(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		q := url.Values{"tab": {tab}, "err": {err.Error()}}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
		return
	}
	pkglog.Component(pkglog.Admin).Info("配置已保存", "section", section, "group", r.FormValue("group"), "keys", len(updates), "need_restart", needRestart)
	if ports.WantsJSON(r) {
		ports.WriteJSON(w, map[string]any{"ok": true, "need_restart": needRestart, "summary": "保存成功"})
		return
	}
	q := url.Values{"tab": {tab}, "ok": {section}}
	if needRestart {
		q.Set("restart", "1")
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	}
	http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
}

// handleConfigExport GET /admin/config/export：当前 KV 导出为 JSON（含敏感项，当备份用）
func (h *Handler) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	kv := CurrentConfigKV(h.opts.DB)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rent-scout-config.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(kv)
}

// handleConfigImport POST /admin/config/import：粘贴 key=value 或 JSON（与导出兼容），覆盖对应项
func (h *Handler) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parseAdminForm(r); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	data, err := readConfigImportPayload(r)
	if err != nil {
		q := url.Values{"tab": {"general"}, "err": {err.Error()}}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
		return
	}
	updates, err := config.ParseImportKV(data)
	if err != nil {
		q := url.Values{"tab": {"general"}, "err": {err.Error()}}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
		return
	}
	needRestart, err := h.saveUpdates(updates)
	if err != nil {
		q := url.Values{"tab": {"general"}, "err": {err.Error()}}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
		return
	}
	pkglog.Component(pkglog.Admin).Info("配置已导入", "keys", len(updates), "need_restart", needRestart)
	q := url.Values{"tab": {"general"}, "ok": {"import"}}
	if needRestart {
		q.Set("restart", "1")
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	}
	http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
}

func readConfigImportPayload(r *http.Request) ([]byte, error) {
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 2<<20))
		if err != nil {
			return nil, err
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, fmt.Errorf("文件为空")
		}
		return data, nil
	}
	data := strings.TrimSpace(r.FormValue("data"))
	if data == "" {
		return nil, fmt.Errorf("请粘贴配置或选择文件")
	}
	return []byte(data), nil
}

// handleConfigHistory 变更历史的只读快照页（从当前 KV 倒放 diff 还原）
func (h *Handler) handleConfigHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "缺少历史 id", http.StatusBadRequest)
		return
	}
	kv, entry, err := store.ReconstructKVAfter(h.opts.DB, id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "历史不存在", http.StatusNotFound)
		return
	}
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("还原历史配置失败", "id", id, "err", err)
		http.Error(w, "还原失败", http.StatusInternalServerError)
		return
	}
	if store.IsSecretConfigKey(entry.Key) {
		if entry.OldValue != "" {
			entry.OldValue = "••••"
		}
		if entry.NewValue != "" {
			entry.NewValue = "••••"
		}
	}
	if err := h.opts.Tmpl.ExecuteTemplate(w, "config_history", ports.MergePageCtx(ports.PageCtx(h.opts.RT, r, "config"), map[string]any{
		"Sections": snapshotSections(kv),
		"Entry":    entry,
	})); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
