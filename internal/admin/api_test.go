package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/config"
	"rent-scout/internal/models"
)

// TestAPIFeedbacksAuth 反馈写入鉴权矩阵（规格 7.1）：
// 管理 token 鉴权下 → 无 sig+Bearer 201；无 token 401；错 sig 401；正确 sig 201；非法 action 400
func TestAPIFeedbacksAuth(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	future := time.Now().Add(time.Hour).Unix()
	goodSig := feedbackSig(1, models.FeedbackUseful, future, "secret")

	cases := []struct {
		name   string
		bearer string // 空 = 不带 Authorization
		sig    string // 空 = 不带签名参数
		action string
		want   int
	}{
		{"无 sig + Bearer", "secret", "", models.FeedbackUseful, http.StatusCreated},
		{"无 token", "", "", models.FeedbackUseful, http.StatusUnauthorized},
		{"错 sig", "secret", "deadbeef", models.FeedbackUseful, http.StatusUnauthorized},
		{"正确 sig", "secret", goodSig, models.FeedbackUseful, http.StatusCreated},
		{"非法 action", "secret", "", "bad", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"post_id":1,"channel":"test","action":%q,"reason":"试试"}`, tc.action)
			req := httptest.NewRequest(http.MethodPost, "/api/feedbacks", strings.NewReader(body))
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.sig != "" {
				q := req.URL.Query()
				q.Set("exp", fmt.Sprintf("%d", future))
				q.Set("sig", tc.sig)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// 两次 201 均真实写库（无 sig 与正确 sig 各一条）
	items, err := s.ListFeedbacksByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Action != models.FeedbackUseful || items[0].Reason != "试试" {
		t.Errorf("DB 反馈 = %+v, want 2 条 useful post=1", items)
	}
}

// TestAPIFeedbacksAuthOff 鉴权关闭（AuthRequired=false）时以开关为准：
// 即使配置了 server token，无 sig / 带 sig 一律放行 201（不验证）；畸形 JSON 仍 400
func TestAPIFeedbacksAuthOff(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{}) // AuthRequired 默认 false
	srv := NewServer(s, rt, "secret", nil)       // 配了 token 也不验证

	future := time.Now().Add(time.Hour).Unix()

	cases := []struct {
		name string
		body string
		sig  string
		want int
	}{
		{"无 sig 放行", `{"post_id":1,"channel":"test","action":"useful"}`, "", http.StatusCreated},
		{"带 sig 放行（不验证）", `{"post_id":1,"channel":"test","action":"useful"}`, "deadbeef", http.StatusCreated},
		{"畸形 JSON", `{bad`, "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/feedbacks", strings.NewReader(tc.body))
			if tc.sig != "" {
				q := req.URL.Query()
				q.Set("exp", fmt.Sprintf("%d", future))
				q.Set("sig", tc.sig)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// 两次 201 均真实写库（无 sig 与带 sig 各一条）
	items, err := s.ListFeedbacksByPost(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Action != models.FeedbackUseful {
		t.Errorf("DB 反馈 = %+v, want 2 条 useful post=1", items)
	}
}

// TestAPIPostsList 列表：status 过滤 / limit+offset 分页 / 空 status 全量
func TestAPIPostsList(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	// 播种 3 帖：passed、rejected、collected（id 倒序 = list2, list1, list0）
	for i, status := range []string{models.PostStatusPassed, models.PostStatusRejected, models.PostStatusCollected} {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("list%d", i), Title: fmt.Sprintf("帖%d", i), Status: status}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) (int, []models.RentPost) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var out struct {
			Posts []models.RentPost `json:"posts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s 解析响应失败: %v (body=%s)", path, err, rec.Body.String())
		}
		return rec.Code, out.Posts
	}

	if code, posts := get("/api/posts?status=passed"); code != http.StatusOK || len(posts) != 1 || posts[0].Status != models.PostStatusPassed {
		t.Errorf("status=passed: code=%d posts=%+v, want 1 条 passed", code, posts)
	}
	if code, posts := get("/api/posts?limit=1&offset=1"); code != http.StatusOK || len(posts) != 1 || posts[0].ExternalID != "list1" {
		t.Errorf("limit=1&offset=1: code=%d posts=%+v, want 1 条 list1（id 倒序第二条）", code, posts)
	}
	if code, posts := get("/api/posts"); code != http.StatusOK || len(posts) != 3 {
		t.Errorf("空 status: code=%d len=%d, want 3 条全量", code, len(posts))
	}
}

// TestAPIPostsListDefaultLimit：不传 limit → 默认 50（播种 55 帖验证默认值生效）
func TestAPIPostsListDefaultLimit(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	for i := 0; i < 55; i++ {
		p := models.RentPost{Source: "douban", ExternalID: fmt.Sprintf("d%d", i), Title: "t", Status: models.PostStatusCollected}
		if _, err := s.InsertPost(p); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Posts []models.RentPost `json:"posts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Posts) != 50 {
		t.Errorf("默认 limit 下返回 %d 条, want 50", len(out.Posts))
	}
}

// TestAPIPostDetail 详情：播种完整链路（post+filter_result+notification+feedback）→ 组合字段齐全；不存在 404
func TestAPIPostDetail(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	if _, err := s.InsertPost(models.RentPost{Source: "douban", ExternalID: "detail1", Title: "详情帖", Status: models.PostStatusPassed}); err != nil {
		t.Fatal(err)
	}
	id := postID(t, s, "detail1")
	if err := s.SaveFilterResult(models.FilterResult{PostID: id, Status: models.PostStatusPassed, Stage: models.StageHardRule, DecidedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertNotification(id, "feishu"); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFeedback(models.Feedback{PostID: id, Channel: "test", Action: models.FeedbackUseful, Reason: "不错", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/posts/%d", id), nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Post         models.RentPost       `json:"post"`
		FilterResult models.FilterResult   `json:"filter_result"`
		Notifications []models.Notification `json:"notifications"`
		Feedbacks    []models.Feedback     `json:"feedbacks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析详情响应失败: %v (body=%s)", err, rec.Body.String())
	}
	if out.Post.ID != id || out.Post.Title != "详情帖" {
		t.Errorf("post 字段 = %+v, want id=%d title=详情帖", out.Post, id)
	}
	if out.FilterResult.Status != models.PostStatusPassed || out.FilterResult.Stage != models.StageHardRule {
		t.Errorf("filter_result = %+v, want passed/hard_rule", out.FilterResult)
	}
	if len(out.Notifications) != 1 || out.Notifications[0].Channel != "feishu" || out.Notifications[0].Status != models.NotifyStatusPending {
		t.Errorf("notifications = %+v, want 1 条 feishu", out.Notifications)
	}
	if len(out.Feedbacks) != 1 || out.Feedbacks[0].Action != models.FeedbackUseful || out.Feedbacks[0].Reason != "不错" {
		t.Errorf("feedbacks = %+v, want 1 条 useful", out.Feedbacks)
	}

	// 不存在 → 404
	req = httptest.NewRequest(http.MethodGet, "/api/posts/99999", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在: status = %d, want 404", rec.Code)
	}
}

// TestAPIPostsEdgeCases：路径/方法边界——空 id 400、非数字 id 400、POST 405
func TestAPIPostsEdgeCases(t *testing.T) {
	s := newAdminTestStore(t)
	defer s.Close()
	rt := config.NewRuntime(&config.AppConfig{Admin: config.AdminConfig{AuthRequired: true}})
	srv := NewServer(s, rt, "secret", nil)

	cases := []struct {
		name string
		path string
		meth string
		want int
	}{
		{"空 id", "/api/posts/", http.MethodGet, http.StatusBadRequest},
		{"非数字 id", "/api/posts/abc", http.MethodGet, http.StatusBadRequest},
		{"列表 POST", "/api/posts", http.MethodPost, http.StatusMethodNotAllowed},
		{"详情 POST", "/api/posts/1", http.MethodPost, http.StatusMethodNotAllowed},
		{"反馈 GET", "/api/feedbacks", http.MethodGet, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.meth, tc.path, nil)
			req.Header.Set("Authorization", "Bearer secret")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
