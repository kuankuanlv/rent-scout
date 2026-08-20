package rules

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rent-scout/internal/admin/ports"
	"rent-scout/internal/models"
	"rent-scout/internal/pkglog"
	"rent-scout/internal/store"
)

// Row 规则列表行：Rule + 命中统计（Hits/UselessCount，规格 5.5）+ 中文类型标签
type Row struct {
	models.Rule
	Hits         int
	UselessCount int
	TypeLabel    string // 白名单/黑名单/AI审核；列表展示用，不强调 mode
}

// ruleTypeLabel type → 中文标签（Spec 09 §2.3 UI）
func ruleTypeLabel(t string) string {
	switch t {
	case models.RuleTypeWhitelist:
		return "白名单"
	case models.RuleTypeBlacklist:
		return "黑名单"
	case models.RuleTypeAINatural:
		return "AI审核"
	default:
		return t
	}
}

// ruleTypes 合法规则类型集合（表单校验用，与 models 常量一致；Spec 09 §2）
var ruleTypes = map[string]bool{
	models.RuleTypeWhitelist: true,
	models.RuleTypeBlacklist: true,
	models.RuleTypeAINatural: true,
}

// handleRules 规则管理（/admin/rules）：GET → 302 到配置 tab=rules；POST 新增（PRG）
func (h *Handler) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.redirectRules(w, r)
	case http.MethodPost:
		h.createRule(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// loadRuleRows 先 ensure 默认规则，再 ListRules(false) 全量 + RuleHitStats 合并；统计失败不阻塞
func LoadRuleRows(db *store.Store) ([]Row, error) {
	log := pkglog.Component(pkglog.Admin)
	if err := db.EnsureDefaultRule(); err != nil {
		log.Error("默认规则播种失败", "err", err)
		return nil, err
	}
	list, err := db.ListRules(false)
	if err != nil {
		log.Error("规则列表失败", "err", err)
		return nil, err
	}
	statsMap := map[int64]store.RuleStat{}
	if stats, err := db.RuleHitStats(); err != nil {
		log.Error("规则统计失败", "err", err)
	} else {
		for _, st := range stats {
			statsMap[st.RuleID] = st
		}
	}
	rows := make([]Row, 0, len(list))
	for _, rl := range list {
		row := Row{Rule: rl, TypeLabel: ruleTypeLabel(rl.Type)}
		if st, ok := statsMap[rl.ID]; ok {
			row.Hits = st.Hits
			row.UselessCount = st.UselessCount
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// createRule 新增规则（POST /admin/rules）：name/type/value/priority 表单 → CreateRule
// 校验：type ∈ 三枚举、value/name 非空、priority 可解析；mode 废弃可忽略；非法 → 400
func (h *Handler) createRule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.PostFormValue("name")
	rtype := r.PostFormValue("type")
	mode := r.PostFormValue("mode") // 废弃：不校验，落库兼容旧表单
	value := r.PostFormValue("value")
	priority, err := strconv.Atoi(r.PostFormValue("priority"))
	if err != nil || name == "" || !ruleTypes[rtype] {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if rtype == models.RuleTypeAINatural {
		value = models.BuiltInAIRuleValue
		if name == "" {
			name = "靠谱个人房源"
		}
	} else if value == "" {
		http.Error(w, "参数无效", http.StatusBadRequest)
		return
	}
	if _, err := h.opts.DB.CreateRule(models.Rule{Name: name, Type: rtype, Mode: mode, Value: value, Enabled: true, Priority: priority}); err != nil {
		pkglog.Component(pkglog.Admin).Error("规则创建失败", "name", name, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("规则已创建", "name", name, "type", rtype, "priority", priority)
	h.notifyRulesChanged()
	h.redirectRules(w, r)
}

// handleRulesID 规则更新/删除（POST /admin/rules/{id} 与 /admin/rules/{id}/delete）
// 仅接受 POST：GET 等请求一律 405，防止 <a>/<img> 链接触发写库。
func (h *Handler) handleRulesID(w http.ResponseWriter, r *http.Request) {
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
		h.deleteRule(w, r, id)
		return
	}
	h.updateRule(w, r, id)
}

// updateRule 更新规则（POST /admin/rules/{id}）：value/priority/enabled 表单 → UpdateRule
// name/type 随行内 hidden 回传（本页不支持改名/改型）；mode 废弃可忽略；enabled 由 checkbox 存在性决定
func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		h.replyRule(w, r, http.StatusBadRequest, "bad form")
		return
	}
	existing, ok, err := h.opts.DB.GetRule(id)
	if err != nil {
		pkglog.Component(pkglog.Admin).Error("规则读取失败", "id", id, "err", err)
		h.replyRule(w, r, http.StatusInternalServerError, "读取失败")
		return
	}
	if !ok {
		h.replyRule(w, r, http.StatusBadRequest, "参数无效")
		return
	}
	rtype := ports.FirstNonEmpty(r.PostFormValue("rule_type"), r.PostFormValue("type"), existing.Type)
	mode := r.PostFormValue("mode")
	if mode == "" {
		mode = existing.Mode
	}
	value := ports.FirstNonEmpty(r.PostFormValue("value"), existing.Value)
	if rtype == models.RuleTypeAINatural {
		value = models.BuiltInAIRuleValue
	}
	name := ports.FirstNonEmpty(r.PostFormValue("name"), existing.Name)
	priority := existing.Priority
	if raw := strings.TrimSpace(r.PostFormValue("priority")); raw != "" {
		priority, err = strconv.Atoi(raw)
		if err != nil {
			h.replyRule(w, r, http.StatusBadRequest, "参数无效")
			return
		}
	}
	enabled := r.PostFormValue("enabled") != ""
	if name == "" || !ruleTypes[rtype] || (rtype != models.RuleTypeAINatural && value == "") {
		h.replyRule(w, r, http.StatusBadRequest, "参数无效")
		return
	}
	updated := models.Rule{ID: id, Name: name, Type: rtype, Mode: mode, Value: value, Enabled: enabled, Priority: priority}
	if err := h.opts.DB.UpdateRule(updated); err != nil {
		if errors.Is(err, store.ErrLastEnabledRule) {
			h.replyRule(w, r, http.StatusBadRequest, err.Error())
			return
		}
		pkglog.Component(pkglog.Admin).Error("规则更新失败", "id", id, "err", err)
		h.replyRule(w, r, http.StatusInternalServerError, "写入失败")
		return
	}
	pkglog.Component(pkglog.Admin).Info("规则已更新", "id", id, "value", value, "priority", priority, "enabled", enabled)
	if RuleNeedsReplay(existing, updated) {
		h.notifyRulesChanged()
	}
	h.replyRuleOK(w, r)
}

func (h *Handler) replyRuleOK(w http.ResponseWriter, r *http.Request) {
	if ports.WantsJSON(r) {
		ports.WriteJSON(w, map[string]any{"ok": true})
		return
	}
	h.redirectRules(w, r)
}

func (h *Handler) replyRule(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if ports.WantsJSON(r) {
		ports.WriteJSONStatus(w, code, map[string]any{"ok": false, "error": msg})
		return
	}
	http.Error(w, msg, code)
}

// deleteRule 删除规则（POST /admin/rules/{id}/delete）
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := h.opts.DB.DeleteRule(id); err != nil {
		if errors.Is(err, store.ErrLastEnabledRule) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pkglog.Component(pkglog.Admin).Error("规则删除失败", "id", id, "err", err)
		http.Error(w, "写入失败", http.StatusInternalServerError)
		return
	}
	pkglog.Component(pkglog.Admin).Info("规则已删除", "id", id)
	h.redirectRules(w, r)
}

// ruleNeedsReplay 更新后是否要重放：只在规则能力变强时（启用、加关键字、改类型）。
// 纯删关键字、禁用、改名/优先级不触发；整条删除在 deleteRule 侧直接不通知。
func RuleNeedsReplay(before, after models.Rule) bool {
	if !after.Enabled {
		return false
	}
	if !before.Enabled {
		return true
	}
	if before.Type != after.Type {
		return true
	}
	if after.Type == models.RuleTypeAINatural {
		return false
	}
	return hasNewRuleKeywords(before.Value, after.Value)
}

func hasNewRuleKeywords(before, after string) bool {
	old := ruleKeywordSet(before)
	for k := range ruleKeywordSet(after) {
		if _, ok := old[k]; !ok {
			return true
		}
	}
	return false
}

// ruleKeywordSet 与硬筛 splitKeywords 同一套分隔符，trim + 小写后做集合比较
func ruleKeywordSet(value string) map[string]struct{} {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '，' || r == '、'
	})
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

// redirectRules GET/PRG：回到配置页规则 Tab；鉴权开启时把 token 带回，避免跳回后 401
func (h *Handler) redirectRules(w http.ResponseWriter, r *http.Request) {
	q := url.Values{"tab": {"rules"}}
	if tok := r.URL.Query().Get("token"); tok != "" {
		q.Set("token", tok)
	}
	status := http.StatusSeeOther
	if r.Method == http.MethodGet {
		status = http.StatusFound
	}
	http.Redirect(w, r, "/admin/config?"+q.Encode(), status)
}
