package admin

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

// ruleRow 规则列表行：Rule + 命中统计（Hits/UselessCount，规格 5.5）
type ruleRow struct {
	models.Rule
	Hits         int
	UselessCount int
}

// ruleTypes 合法规则类型集合（表单校验用，与 models 常量一致）
var ruleTypes = map[string]bool{
	models.RuleTypeHardKeyword:   true,
	models.RuleTypeHardBlacklist: true,
	models.RuleTypeHardWhitelist: true,
	models.RuleTypeAINatural:     true,
}

// handleRules 规则管理（/admin/rules）：GET 列表页（含命中统计），POST 新增（PRG）
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.renderRules(w, r)
	case http.MethodPost:
		s.createRule(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderRules 渲染规则列表页：ListRules(false) 全量 + RuleHitStats 命中统计合并到行数据
func (s *Server) renderRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.db.ListRules(false)
	if err != nil {
		slog.Error("规则列表失败", "err", err)
		http.Error(w, "查询失败", http.StatusInternalServerError)
		return
	}
	// 命中统计失败不阻塞列表（仅记录日志）
	statsMap := map[int64]store.RuleStat{}
	if stats, err := s.db.RuleHitStats(); err != nil {
		slog.Error("规则命中统计失败", "err", err)
	} else {
		for _, st := range stats {
			statsMap[st.RuleID] = st
		}
	}
	rows := make([]ruleRow, 0, len(rules))
	for _, rl := range rules {
		row := ruleRow{Rule: rl}
		if st, ok := statsMap[rl.ID]; ok {
			row.Hits = st.Hits
			row.UselessCount = st.UselessCount
		}
		rows = append(rows, row)
	}
	if err := s.tmpl.ExecuteTemplate(w, "rules", map[string]any{"Rules": rows, "Token": r.URL.Query().Get("token")}); err != nil {
		slog.Error("模板渲染失败", "err", err)
	}
}

// createRule 新增规则（POST /admin/rules）：name/type/mode/value/priority 表单 → CreateRule
// 校验：type ∈ 四枚举、mode ∈ {include, exclude}、value/name 非空、priority 可解析；非法 → 400
func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.PostFormValue("name")
	rtype := r.PostFormValue("type")
	mode := r.PostFormValue("mode")
	value := r.PostFormValue("value")
	priority, err := strconv.Atoi(r.PostFormValue("priority"))
	if err != nil || name == "" || !ruleTypes[rtype] ||
		(mode != models.RuleModeInclude && mode != models.RuleModeExclude) || value == "" {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if _, err := s.db.CreateRule(models.Rule{Name: name, Type: rtype, Mode: mode, Value: value, Enabled: true, Priority: priority}); err != nil {
		slog.Error("新增规则失败", "name", name, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	slog.Info("rule_created", "name", name, "type", rtype, "mode", mode, "priority", priority)
	s.redirectRules(w, r)
}

// handleRulesID 规则更新/删除（POST /admin/rules/{id} 与 /admin/rules/{id}/delete）
// 仅接受 POST：GET 等请求一律 405，防止 <a>/<img> 链接触发写库。
func (s *Server) handleRulesID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/admin/rules/")
	isDelete := strings.HasSuffix(rest, "/delete")
	if isDelete {
		rest = strings.TrimSuffix(rest, "/delete")
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if isDelete {
		s.deleteRule(w, r, id)
		return
	}
	s.updateRule(w, r, id)
}

// updateRule 更新规则（POST /admin/rules/{id}）：value/priority/enabled 表单 → UpdateRule
// name/type/mode 随行内 hidden 回传（本页不支持改名/改型）；enabled 由 checkbox 存在性决定
func (s *Server) updateRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rtype := r.PostFormValue("type")
	mode := r.PostFormValue("mode")
	value := r.PostFormValue("value")
	priority, err := strconv.Atoi(r.PostFormValue("priority"))
	enabled := r.PostFormValue("enabled") != ""
	if err != nil || !ruleTypes[rtype] ||
		(mode != models.RuleModeInclude && mode != models.RuleModeExclude) || value == "" {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if err := s.db.UpdateRule(models.Rule{ID: id, Name: r.PostFormValue("name"), Type: rtype, Mode: mode, Value: value, Enabled: enabled, Priority: priority}); err != nil {
		slog.Error("更新规则失败", "id", id, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	slog.Info("rule_updated", "id", id, "value", value, "priority", priority, "enabled", enabled)
	s.redirectRules(w, r)
}

// deleteRule 删除规则（POST /admin/rules/{id}/delete）
func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := s.db.DeleteRule(id); err != nil {
		slog.Error("删除规则失败", "id", id, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	slog.Info("rule_deleted", "id", id)
	s.redirectRules(w, r)
}

// redirectRules PRG：写操作成功后 302 回规则列表；鉴权开启时把 token 带回重定向目标，避免跳回后 401
func (s *Server) redirectRules(w http.ResponseWriter, r *http.Request) {
	redirectTo := "/admin/rules"
	if tok := r.URL.Query().Get("token"); tok != "" {
		redirectTo += "?token=" + tok
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}
