package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/actionref"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

func pref(id int64, secret string) string {
	return actionref.Seal(id, secret)
}

func fRef(id int64, action, secret, extra string) string {
	u := "/f?p=" + pref(id, secret) + "&action=" + action
	if extra != "" {
		u += extra
	}
	return u
}

func hRef(id int64, secret, extra string) string {
	u := "/h?p=" + pref(id, secret)
	if extra != "" {
		u += extra
	}
	return u
}

// feedbackSig 生成反馈链接签名（与 notifier.BuildFeedbackURL 同算法，供测试构造合法链接）
func feedbackSig(postID int64, action string, exp int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d|%s|%d", postID, action, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestFeedbackNoToken：token 空（鉴权关闭）→ 不签名直接放行，任意合法参数 200
func TestFeedbackNoToken(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "", nil)

	req := httptest.NewRequest(http.MethodGet, fRef(1, "useful", "", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200（不签名模式）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("body 缺成功文案: %s", rec.Body.String())
	}
}

// TestFeedbackSigned：token 非空 → 无 sig/过期/错 sig 失败页；正确签名 200 + 写库；重复点击两次 200
func TestFeedbackSigned(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	// 无 sig → 失败页
	req := httptest.NewRequest(http.MethodGet, fRef(1, "useful", "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("无 sig: status = %d, want 200（失败页）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("无 sig: body 缺失败文案: %s", rec.Body.String())
	}

	// 过期 exp（过去时间戳）→ 失败页
	expired := time.Now().Add(-time.Hour).Unix()
	req = httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=%s", expired, feedbackSig(1, "useful", expired, "secret"))), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("过期: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 错误 sig（exp 未来有效，排除过期干扰）→ 失败页
	future := time.Now().Add(time.Hour).Unix()
	req = httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=deadbeef", future)), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("错 sig: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 正确签名 → 200 + 成功文案 + DB 有记录
	sig := feedbackSig(1, "useful", future, "secret")
	url := fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=%s", future, sig))
	req = httptest.NewRequest(http.MethodGet, url, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("正确签名: status=%d body=%s", rec.Code, rec.Body.String())
	}
	tags, err := s.ListTagsByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].Kind != models.TagKindFeedback || tags[0].Text != "有用" {
		t.Errorf("DB 标签 = %+v, want 1 条 useful post=1", tags)
	}

	// 重复点击同一签名 → 两次都 200（v1 接受重复记录，报表按帖去重——RuleHitStats 已 COUNT(DISTINCT post_id)）
	for i := 0; i < 2; i++ {
		req = httptest.NewRequest(http.MethodGet, url, nil)
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("重复点击 #%d: status = %d, want 200", i+1, rec.Code)
		}
	}
}

// TestFeedbackBadAction：非法 action → 400
func TestFeedbackBadAction(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, fRef(1, "bad", "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestFeedbackAuthDisabled：AuthRequired=false → 无效签名也放行（开关为准）
func TestFeedbackAuthDisabled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()

	// 鉴权关闭 + token 非空（模拟有 token 但开关关闭的场景）
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: false}}, "secret", nil)

	// 无效签名 → 应放行（开关为准）
	future := time.Now().Add(time.Hour).Unix()
	req := httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=invalid", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=false 无效签名: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("AuthRequired=false 无效签名: body 缺成功文案: %s", rec.Body.String())
	}

	// 完全无 sig → 也应放行
	req = httptest.NewRequest(http.MethodGet, fRef(2, "useless", "secret", ""), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=false 无sig: status = %d, want 200", rec.Code)
	}
}

// TestFeedbackAuthEnabledInvalidSig：AuthRequired=true + token 非空 → 无效签名被拒绝
func TestFeedbackAuthEnabledInvalidSig(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()

	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	// 无效签名 → 应拒绝
	future := time.Now().Add(time.Hour).Unix()
	req := httptest.NewRequest(http.MethodGet,
		fRef(1, "useful", "secret", fmt.Sprintf("&exp=%d&sig=invalid", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("AuthRequired=true 无效签名: status = %d, want 200（失败页）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("AuthRequired=true 无效签名: body 缺失败文案: %s", rec.Body.String())
	}
}

// TestHandledLinkSigned：验签成功写 handled_at；错签失败；不写 feedbacks（Spec 09 §3.5）
func TestHandledLinkSigned(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h1", Title: "已处理帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "h1")
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}}, "secret", nil)

	future := time.Now().Add(time.Hour).Unix()

	// 错签 → 失败页，HandledAt 仍空，无反馈
	req := httptest.NewRequest(http.MethodGet,
		hRef(id, "secret", fmt.Sprintf("&exp=%d&sig=deadbeef", future)), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("错签: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt != nil {
		t.Fatalf("错签后不应写 HandledAt: ok=%v err=%v HandledAt=%v", ok, err, p.HandledAt)
	}
	tags, err := s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("错签不应写标签: %d", len(tags))
	}

	// 正确签名 → 成功 + HandledAt + 仍无 feedbacks
	sig := feedbackSig(id, "handled", future, "secret")
	req = httptest.NewRequest(http.MethodGet,
		hRef(id, "secret", fmt.Sprintf("&exp=%d&sig=%s", future, sig)), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已标记为已处理") {
		t.Errorf("正签: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err = s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("正签后应写 HandledAt: ok=%v err=%v", ok, err)
	}
	tags, err = s.ListTagsByPost(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("已处理入口不应写标签: %+v", tags)
	}
}

// TestHandledLinkAuthDisabled：鉴权关 → 无签也可标记，仍不写 feedbacks
func TestHandledLinkAuthDisabled(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "h2", Title: "开放已处理", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "h2")
	srv := newTestServerWithStore(t, s, &config.AppConfig{Admin: config.AdminConfig{AuthRequired: false}}, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, hRef(id, "secret", ""), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "已标记为已处理") {
		t.Errorf("鉴权关: status=%d body=%s", rec.Code, rec.Body.String())
	}
	p, ok, err := s.GetPost(id)
	if err != nil || !ok || p.HandledAt == nil {
		t.Fatalf("鉴权关应写 HandledAt: ok=%v err=%v", ok, err)
	}
	tags, err := s.ListTagsByPost(id)
	if err != nil || len(tags) != 0 {
		t.Errorf("不应写标签: err=%v n=%d", err, len(tags))
	}
}
