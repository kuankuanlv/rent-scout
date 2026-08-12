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

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

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
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/f?post=1&action=useful", nil)
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
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	// 无 sig → 失败页
	req := httptest.NewRequest(http.MethodGet, "/f?post=1&action=useful", nil)
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
		fmt.Sprintf("/f?post=1&action=useful&exp=%d&sig=%s", expired, feedbackSig(1, "useful", expired, "secret")), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("过期: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 错误 sig（exp 未来有效，排除过期干扰）→ 失败页
	future := time.Now().Add(time.Hour).Unix()
	req = httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/f?post=1&action=useful&exp=%d&sig=deadbeef", future), nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "无效或已过期") {
		t.Errorf("错 sig: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 正确签名 → 200 + 成功文案 + DB 有记录
	sig := feedbackSig(1, "useful", future, "secret")
	url := fmt.Sprintf("/f?post=1&action=useful&exp=%d&sig=%s", future, sig)
	req = httptest.NewRequest(http.MethodGet, url, nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "感谢反馈") {
		t.Errorf("正确签名: status=%d body=%s", rec.Code, rec.Body.String())
	}
	items, err := s.ListFeedbacksByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].PostID != 1 || items[0].Action != models.FeedbackUseful {
		t.Errorf("DB 反馈 = %+v, want 1 条 useful post=1", items)
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
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	req := httptest.NewRequest(http.MethodGet, "/f?post=1&action=bad", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
