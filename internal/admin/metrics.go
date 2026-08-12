package admin

import (
	"fmt"
	"net/http"
)

// handleHealthz 健康检查（无鉴权）
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleMetrics 基础指标（规格 7.4：Prometheus 文本格式，无鉴权——监控抓取）
// 数据来自 store.TodayStats / ChannelStats（任务 2）
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	stats, err := s.db.TodayStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("今日统计失败: %v", err), http.StatusInternalServerError)
		return
	}
	channels, err := s.db.ChannelStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("渠道统计失败: %v", err), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "# HELP rent_scout_posts_collected_total 今日采集帖子总数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_collected_total counter\n")
	fmt.Fprintf(w, "rent_scout_posts_collected_total %d\n", stats.Collected)
	fmt.Fprintf(w, "# HELP rent_scout_posts_passed_total 今日筛选通过帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_passed_total counter\n")
	fmt.Fprintf(w, "rent_scout_posts_passed_total %d\n", stats.Passed)
	fmt.Fprintf(w, "# HELP rent_scout_posts_rejected_total 今日筛选拒绝帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_rejected_total counter\n")
	fmt.Fprintf(w, "rent_scout_posts_rejected_total %d\n", stats.Rejected)
	fmt.Fprintf(w, "# HELP rent_scout_posts_pending_total 今日待判定帖子数\n")
	fmt.Fprintf(w, "# TYPE rent_scout_posts_pending_total counter\n")
	fmt.Fprintf(w, "rent_scout_posts_pending_total %d\n", stats.Pending)

	fmt.Fprintf(w, "# HELP rent_scout_notify_sent_total 渠道发送总数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_sent_total counter\n")
	fmt.Fprintf(w, "# HELP rent_scout_notify_failed_total 渠道发送失败数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_failed_total counter\n")
	fmt.Fprintf(w, "# HELP rent_scout_notify_dead_total 渠道死信数（历史累计）\n")
	fmt.Fprintf(w, "# TYPE rent_scout_notify_dead_total counter\n")
	for _, c := range channels {
		fmt.Fprintf(w, "rent_scout_notify_sent_total{channel=%q} %d\n", c.Channel, c.Sent)
		fmt.Fprintf(w, "rent_scout_notify_failed_total{channel=%q} %d\n", c.Channel, c.Failed)
		fmt.Fprintf(w, "rent_scout_notify_dead_total{channel=%q} %d\n", c.Channel, c.Dead)
	}
}
