package xiaohongshu

import (
	"net/http"
	"testing"
)

func TestParseSearchNotes(t *testing.T) {
	items, err := ParseSearchNotes([]byte(`{"data":{"items":[{"id":"n1","note_card":{"display_title":"北京整租","desc":"地铁附近 3500/月","user":{"user_id":"u1","nickname":"房东"},"time":1735689600000}}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ExternalID != "n1" || items[0].Author != "房东" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].PublishedAt.IsZero() || items[0].Content == "" {
		t.Fatalf("incomplete item = %+v", items[0])
	}
}

func TestClassifyRiskResponse(t *testing.T) {
	tests := []struct {
		code int
		err  bool
	}{
		{http.StatusOK, false},
		{461, true},
		{406, true},
		{401, true},
		{500, true},
	}

	for _, tt := range tests {
		err := ClassifyError(&http.Response{StatusCode: tt.code})
		if (err != nil) != tt.err {
			t.Errorf("code %d: expected err=%v, got %v", tt.code, tt.err, err)
		}
	}
}
