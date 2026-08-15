package douban

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rent-scout/internal/collector"
	"rent-scout/internal/config"
	"rent-scout/internal/models"
	"rent-scout/internal/store"
)

func TestRunnerDoubanBackfillThenIncremental(t *testing.T) {
	now := time.Now()
	t0 := now.Add(-time.Hour).In(time.Local).Format("2006-01-02 15:04")
	t1 := now.Add(-48 * time.Hour).In(time.Local).Format("2006-01-02 15:04")

	var listStarts []string
	var detailHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		if strings.Contains(r.URL.Path, "/discussion") {
			start := r.URL.Query().Get("start")
			listStarts = append(listStarts, start)
			switch start {
			case "", "0":
				fmt.Fprintf(w, `<html><table class="olt"><tr><th>t</th></tr>
<tr><td class="title"><a href="%s/group/topic/111/" title="新帖"></a></td>
<td><a>u1</a></td><td class="time">%s</td></tr></table></html>`, base, t0)
			case "25":
				fmt.Fprintf(w, `<html><table class="olt"><tr><th>t</th></tr>
<tr><td class="title"><a href="%s/group/topic/222/" title="旧帖"></a></td>
<td><a>u2</a></td><td class="time">%s</td></tr></table></html>`, base, t1)
			default:
				w.Write([]byte(`<html><table class="olt"><tr><th>t</th></tr></table></html>`))
			}
			return
		}
		if strings.Contains(r.URL.Path, "/topic/") {
			detailHits++
			w.Write([]byte(`<html><div class="topic-content"><p>正文</p></div></html>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	src, err := NewDouban(DoubanOptions{
		GroupURLs: []string{srv.URL + "/group/x/discussion"},
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	group := srv.URL + "/group/x/discussion"
	rt := config.NewHotConfigWithSnapshot(&config.AppConfig{
		Collector: config.CollectorConfig{
			Interval: 3600, JitterRatio: 0, MaxAgeDays: 7,
			Sources: []string{"douban"},
			Douban: config.DoubanConfig{
				Groups:    []string{group},
				RangeFrom: "-10d",
				RangeTo:   "now",
			},
		},
	}, nil)
	r := collector.NewRunner(rt, st, []collector.Source{src}, nil)

	if err := r.RunOnce(context.Background(), src, make(chan struct{}, 4)); err != nil {
		t.Fatal(err)
	}
	if len(listStarts) < 2 {
		t.Fatalf("回填列表请求 = %v, 至少应打 start=0 和 25", listStarts)
	}
	if detailHits != 2 {
		t.Fatalf("回填 Detail = %d, want 2", detailHits)
	}
	prog, ok, err := st.GetProgress("douban")
	if err != nil || !ok || !prog.CatchingUp() {
		t.Fatalf("回填后进度 = %+v ok=%v err=%v", prog, ok, err)
	}
	nList := len(listStarts)

	if err := r.RunOnce(context.Background(), src, make(chan struct{}, 4)); err != nil {
		t.Fatal(err)
	}
	if extra := len(listStarts) - nList; extra != 1 {
		t.Errorf("增量多打了 %d 次列表 %v, want 仅首页 1 次", extra, listStarts[nList:])
	}
	if detailHits != 2 {
		t.Errorf("增量后又打 Detail，总 %d, want 仍 2", detailHits)
	}
	batch, err := st.FetchPendingByStatus(models.PostStatusCollected, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Errorf("入库 = %d, want 2", len(batch))
	}
}
