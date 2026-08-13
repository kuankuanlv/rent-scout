package admin

import (
	"encoding/json"
	"net/http"
	"net/url"

	"rent-scout/internal/config"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// handleConfig 配置管理页与各分区独立保存（二级 Tab：仅渲染当前 tab）
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/admin/config/save" {
		s.handleConfigSectionSave(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tab := normalizeConfigTab(r.URL.Query().Get("tab"))
	data := mergePageCtx(pageCtx(r, "config"), map[string]any{
		"Tab":  tab,
		"Tabs": configTabs,
	})
	if ok := r.URL.Query().Get("ok"); ok != "" {
		data["Message"] = "分区「" + ok + "」已保存"
	}
	if r.URL.Query().Get("restart") == "1" {
		data["NeedRestart"] = true
	}
	if msg := r.URL.Query().Get("err"); msg != "" {
		data["Error"] = msg
	}

	if tab == "rules" {
		rows, err := loadRuleRows(s.db)
		if err != nil {
			http.Error(w, "查询失败", http.StatusInternalServerError)
			return
		}
		data["Rules"] = rows
	} else {
		kv := CurrentConfigKV(s.db)
		app := s.rt.Get()
		env := s.rt.Secrets()
		secID := tabToSectionID(tab)
		sec := sectionByID(buildConfigSections(app, env, kv), secID)
		if sec == nil {
			http.Error(w, "未知配置分区", http.StatusBadRequest)
			return
		}
		data["Section"] = sec
		if tab == "general" {
			history, _ := store.ListConfigHistory(s.db, 20)
			data["History"] = history
		}
	}

	if err := s.tmpl.ExecuteTemplate(w, "config", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleConfigSectionSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	section := r.FormValue("section")
	if _, ok := config.SectionKeys[section]; !ok {
		http.Error(w, "未知配置分区", http.StatusBadRequest)
		return
	}
	tab := sectionIDToTab(section)
	rawKV, _ := store.GetConfigMap(s.db)
	updates := ParseSectionForm(r.Form, section, rawKV)
	needRestart := len(ChangedRestartKeys(rawKV, updates)) > 0
	if err := s.saveSectionUpdates(section, updates); err != nil {
		q := url.Values{"tab": {tab}, "err": {err.Error()}}
		if tok := r.URL.Query().Get("token"); tok != "" {
			q.Set("token", tok)
		}
		http.Redirect(w, r, "/admin/config?"+q.Encode(), http.StatusSeeOther)
		return
	}
	pkglog.Component(pkglog.Admin).Info("配置已保存", "section", section, "keys", len(updates), "need_restart", needRestart)
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
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	kv := CurrentConfigKV(s.db)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rent-scout-config.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(kv)
}
