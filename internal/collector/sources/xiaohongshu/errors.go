package xiaohongshu

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrUnrecoverable = errors.New("unrecoverable error")
)

func ClassifyError(resp *http.Response) error {
	if resp == nil {
		return nil
	}
	switch resp.StatusCode {
	case 461:
		return ErrUnrecoverable
	case 406:
		return fmt.Errorf("signature error: %d", resp.StatusCode)
	case 401:
		// 登录墙
		return fmt.Errorf("login wall: %d", resp.StatusCode)
	case http.StatusOK:
		return nil
	default:
		return fmt.Errorf("request error: %d", resp.StatusCode)
	}
}
