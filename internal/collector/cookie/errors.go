package cookie

import "errors"

var (
	// ErrCookieMissing 本地 cookie_raw 空（raw / cookiecloud 模式都只读本地）
	ErrCookieMissing = errors.New("本地 cookie 为空")
	// ErrCookieInvalid 豆瓣页判定 cookie 失效（风控/登录墙）
	ErrCookieInvalid = errors.New("cookie 已失效")
)
